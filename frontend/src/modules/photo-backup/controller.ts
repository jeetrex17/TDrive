import { get, writable } from 'svelte/store';
import {
    addPhotoBackupFolder, defaultSettings, enqueuePhotoBackupAssets, getPhotoBackupState, normalizeAsset,
    pausePhotoBackup, removePhotoBackupSource, resolvePhotoBackupResource, resumePhotoBackup, retryPhotoBackup, setPhotoBackupPolicy,
    runPhotoBackup, savePhotoBackupSettings, type PhotoBackupSettings, type PhotoBackupSource,
    type PhotoBackupState, upsertPhotoBackupSource,
} from '../../api/photo-backup';
import { OperationFailure, requireOperationSuccess } from '../../api/operation';
import { onRuntimeEvent, runtimeEventsAvailable, type RuntimeUnsubscribe } from '../../api/runtime';
import { asRecord, boundedText } from '../../api/shared';
import type { OperationError, OperationResult } from '../../types';
import { listNativePhotoBackupAssets, listNativePhotoBackupSources, materializeNativePhotoBackupAsset, nativeErrorCode, nativePhotoBackupAvailable, nativePhotoBackupFolderPicking, pickNativePhotoBackupFolder, releaseNativePhotoBackupAsset, requestNativePhotoBackupAccess, nativePhotoBackupPolicy } from './native-adapter';
import { activeDrive } from '../../ui/mobile/mobile-shell-store';
import { activatePhotoBackupBackground } from './background';
import { callWithPasswordRetry, openEncryptionPasswordModal } from '../modals/encryption-password';
import { humanizeBackendError } from '../errors';
import { clearPhotoBackupActivity, syncPhotoBackupActivity } from './activity';

export const photoBackupState = writable<PhotoBackupState | null>(null);
export const photoBackupError = writable('');
export const photoBackupBusy = writable(false);
export const photoBackupCandidates = writable<PhotoBackupSource[]>([]);
// What the device says about the media grant. It lives here rather than in the
// backend state because only the host can see an Android permission, and under
// partial access it is the one thing that explains why the list of albums is
// nearly empty.
export const photoBackupAccessNote = writable('');

const UNLOCK_MESSAGE = 'Unlock encryption to continue photo backup.';
/** How long to sit on a device-side wait (Wi-Fi, policy) before asking again. */
const POLICY_RECHECK_MS = 30_000;
/** How long to sit on a native access or provider failure before trying again. */
const ACCESS_RETRY_MS = 5_000;

let active = false;
let refreshTimer: ReturnType<typeof setTimeout> | null = null;
let schedulerRunning = false;
let schedulerEpoch = 0;
// A backend reply belongs to the account and drive that requested it. Changing
// either scope invalidates replies already in flight so an old filename cannot
// reappear in the bell after a switch.
let scopeEpoch = 0;
let retryTimer: ReturnType<typeof setTimeout> | null = null;
let manuallyPaused = false;
let documentVisible = typeof document === 'undefined' || document.visibilityState === 'visible';
let observedDriveID: number | null = null;
let policySampling = false;
const completedScans = new Set<string>();
const discoveryCursors = new Map<string, string>();
const materializedResources = new Map<string, string>();
const materializations = new Map<string, AbortController>();

// The backend cannot see the page walking the library, so the scanning phase
// is layered on here: the last backend snapshot plus one flag. Everything that
// reads state -- the panel and the activity row alike -- sees the same view.
let backendState: PhotoBackupState | null = null;
let discovering = false;

function publish(state: PhotoBackupState | null): void {
    backendState = state;
    const idle = state?.status.phase === 'idle' || state?.status.phase === 'queued' || state?.status.phase === 'complete';
    const view = state && discovering && idle && !state.manualPaused
        ? { ...state, status: { ...state.status, phase: 'scanning' as const } }
        : state;
    photoBackupState.set(view);
    if (view) syncPhotoBackupActivity(view);
}

function setDiscovering(value: boolean): void {
    if (discovering === value) return;
    discovering = value;
    publish(backendState);
}

export async function refreshPhotoBackup(): Promise<void> {
    const epoch = scopeEpoch;
    try {
        const state = await getPhotoBackupState();
        if (epoch !== scopeEpoch) return;
        manuallyPaused = state.manualPaused;
        publish(state);
        photoBackupError.set('');
    }
    catch { if (epoch === scopeEpoch) photoBackupError.set('Photo backup is unavailable. Try again.'); }
}

function scheduleRefresh(): void {
    if (refreshTimer) return;
    refreshTimer = setTimeout(() => { refreshTimer = null; void refreshPhotoBackup(); }, 120);
}

function cancelDiscovery(): void {
    schedulerEpoch += 1;
    discoveryCursors.clear();
    completedScans.clear();
    if (retryTimer) { clearTimeout(retryTimer); retryTimer = null; }
}

function retryDiscoveryAfter(delay: number, epoch: number): void {
    if (!active || epoch !== schedulerEpoch) return;
    retryTimer = setTimeout(() => { retryTimer = null; void runDiscoveryScheduler(); }, delay);
}

const yieldToForeground = () => new Promise<void>((resolve) => setTimeout(resolve, 0));

async function refreshNativePolicy(force = false): Promise<void> {
    if (policySampling || !active || !documentVisible || (manuallyPaused && !force)) return;
    policySampling = true;
    try {
        const wifi = await nativePhotoBackupPolicy();
        if (wifi !== null && active && documentVisible) await setPhotoBackupPolicy(wifi);
    } finally { policySampling = false; }
}

/** Shown when the backend declines to run: its own words, or the unlock hint for a locked vault. */
function refusalMessage(error: OperationError): string {
    return error.code === 'encryption_password_required' ? UNLOCK_MESSAGE : humanizeBackendError(error);
}

// A single serial worker gives native staging and the durable enqueue a bounded
// concurrency of one. It yields after every <=128 asset page so a huge library
// does not monopolize the WebView, but continues automatically while active.
async function runDiscoveryScheduler(): Promise<void> {
    if (schedulerRunning || !active || !documentVisible || manuallyPaused || !nativePhotoBackupAvailable()) return;
    schedulerRunning = true;
    const epoch = schedulerEpoch;
    const currentScope = scopeEpoch;
    try {
        const current = await getPhotoBackupState();
        if (currentScope !== scopeEpoch) return;
        manuallyPaused = current.manualPaused;
        if (!current.settings.enabled || epoch !== schedulerEpoch || manuallyPaused) return;
        if (current.encryptionRequired) {
            photoBackupError.set(UNLOCK_MESSAGE);
            return;
        }
        await refreshNativePolicy();
        if (epoch !== schedulerEpoch) return;
        // Drain persisted work even when discovery already reached the end. A
        // refusal is a state to show rather than an error to retry into: a
        // locked vault waits for the user, a Wi-Fi wait for the device, and
        // the device is asked again after a while.
        const drained = await runPhotoBackup();
        if (!drained.ok) {
            if (epoch !== schedulerEpoch) return;
            photoBackupError.set(refusalMessage(drained.error));
            if (drained.error.code === 'operation_failed') retryDiscoveryAfter(POLICY_RECHECK_MS, epoch);
            return;
        }
        setDiscovering(true);
        for (const source of current.sources.filter((item) => item.enabled)) {
            if (completedScans.has(source.id)) continue;
            while (active && epoch === schedulerEpoch) {
                const before = discoveryCursors.get(source.id) ?? '';
                const page = await listNativePhotoBackupAssets(source.id, before);
                if (epoch !== schedulerEpoch) return;
                if (page.assets.length) await enqueuePhotoBackupAssets(source.id, page.assets);
                if (epoch !== schedulerEpoch) return;
                const next = page.nextCursor;
                if (next && next === before) throw new Error('Photo library cursor did not advance.');
                if (next) discoveryCursors.set(source.id, next); else discoveryCursors.delete(source.id);
                await runPhotoBackup();
                if (!next) { completedScans.add(source.id); break; }
                await yieldToForeground();
            }
        }
        await refreshPhotoBackup();
    } catch {
        if (active && epoch === schedulerEpoch) photoBackupError.set('Backup is waiting for media access or a connection.');
        // Native permission and transient provider failures are retried while
        // foregrounded without presenting an endless stream of errors.
        retryDiscoveryAfter(ACCESS_RETRY_MS, epoch);
    } finally {
        setDiscovering(false);
        schedulerRunning = false;
    }
}

// Asking for access every time is deliberate. A user who chose "Select photos"
// can only widen that from the system's own dialog, and this button is the
// place they come to when the list is missing what they expected.
export async function loadPhotoBackupCandidates(): Promise<void> {
    if (!nativePhotoBackupAvailable()) return;
    photoBackupBusy.set(true); photoBackupError.set('');
    try {
        await requestNativePhotoBackupAccess();
        const listing = await listNativePhotoBackupSources();
        photoBackupCandidates.set(listing.sources);
        photoBackupAccessNote.set(listing.access.status === 'limited' || listing.access.status === 'denied' ? listing.access.detail : '');
    } catch (cause) {
        // iOS answers a refused library with its own code rather than an empty
        // list; unhandled, that was a tap that did nothing.
        photoBackupError.set(nativeErrorCode(cause) === 'photoAccessRequired'
            ? 'Allow TDrive to access your photos in Settings, then try again.'
            : 'Could not read your photo library. Try again.');
    } finally { photoBackupBusy.set(false); }
}
/**
 * Whether a folder can be added on this host. Desktop always can, through the
 * backend's own dialog; a phone only where the host offers a picker, which is
 * Android. iOS has no folder to offer -- the photo library is the unit there,
 * and the sandbox has no user-visible tree to watch -- so the button is absent
 * rather than present and refusing.
 */
export function photoBackupFolderPicking(): boolean { return !nativePhotoBackupAvailable() || nativePhotoBackupFolderPicking(); }

export async function selectPhotoBackupSource(source: PhotoBackupSource): Promise<void> { await upsertPhotoBackupSource(source); await refreshPhotoBackup(); void runDiscoveryScheduler(); }

// An explicit action may open the existing password modal, then runs once. The
// snapshot can be stale, so the backend's stable locked-vault code is the
// second chance: the shared retry helper unlocks and re-runs exactly once.
// Resolves false when the user dismissed the prompt, which is not a failure.
async function runUnlocked(action: () => Promise<OperationResult>): Promise<boolean> {
    if (get(photoBackupState)?.encryptionRequired && !await openEncryptionPasswordModal()) return false;
    const result = await callWithPasswordRetry(action);
    if (result.ok) return true;
    if (result.error.code === 'canceled') return false;
    throw new OperationFailure(result.error);
}

// The backend's refusal is worth repeating verbatim -- "Waiting for Wi-Fi." tells
// the user what to do. A transport failure is not, so it gets the generic copy.
function reportFailure(cause: unknown, fallback: string): void {
    photoBackupError.set(cause instanceof OperationFailure ? humanizeBackendError(cause) : fallback);
}

export async function startPhotoBackup(): Promise<void> {
    manuallyPaused = false; photoBackupBusy.set(true); photoBackupError.set('');
    try {
        if (await runUnlocked(async () => { await refreshNativePolicy(true); return runPhotoBackup(); })) { await refreshPhotoBackup(); void runDiscoveryScheduler(); }
    } catch (cause) { reportFailure(cause, 'Could not start photo backup. Try again.'); }
    finally { photoBackupBusy.set(false); }
}

export async function updatePhotoBackupSettings(settings: PhotoBackupSettings): Promise<void> {
    cancelDiscovery(); photoBackupBusy.set(true); photoBackupError.set('');
    try { publish(await savePhotoBackupSettings({ ...defaultSettings, ...settings })); if (settings.enabled) void runDiscoveryScheduler(); }
    catch { photoBackupError.set('Could not save backup settings. Try again.'); }
    finally { photoBackupBusy.set(false); }
}

/**
 * Add a folder to back up.
 *
 * Desktop opens the native directory dialog inside the backend. A phone cannot:
 * the picker is an activity, and what it returns is a document tree the backend
 * could not read, so the host picks the folder and hands back the source it
 * became. Both ends arrive at the same place -- one more row in the ledger.
 *
 * The host's refusals are written for the user ("choose one in internal storage
 * or on the SD card"), so they are shown as they are.
 */
export async function choosePhotoBackupFolder(): Promise<void> {
    photoBackupBusy.set(true);
    try {
        if (nativePhotoBackupFolderPicking()) {
            const picked = await pickNativePhotoBackupFolder();
            if (!picked) return;
            await upsertPhotoBackupSource(picked);
        } else {
            await addPhotoBackupFolder();
        }
        await refreshPhotoBackup();
        void runDiscoveryScheduler();
    }
    catch (cause) { photoBackupError.set(folderRefusal(cause)); }
    finally { photoBackupBusy.set(false); }
}

// Both ends refuse a folder in words meant for the user; only the backend's
// "photo backup:" log prefix has to come off before they are shown.
function folderRefusal(cause: unknown): string {
    const message = cause instanceof Error ? cause.message.replace(/^photo backup:\s*/i, '').trim() : '';
    return message ? message.slice(0, 240) : 'Could not add that folder. Try again.';
}

export async function deletePhotoBackupSource(id: string): Promise<void> { cancelDiscovery(); photoBackupBusy.set(true); try { await removePhotoBackupSource(id); await refreshPhotoBackup(); } catch { photoBackupError.set('Could not remove that source. Try again.'); } finally { photoBackupBusy.set(false); } }
export async function pausePhotoBackupNow(): Promise<void> {
    manuallyPaused = true; cancelDiscovery();
    for (const controller of materializations.values()) controller.abort();
    photoBackupBusy.set(true); photoBackupError.set('');
    try { requireOperationSuccess(await pausePhotoBackup()); await refreshPhotoBackup(); }
    catch (cause) { await refreshPhotoBackup(); reportFailure(cause, 'Could not pause photo backup. Try again.'); }
    finally { photoBackupBusy.set(false); }
}
export async function resumePhotoBackupNow(): Promise<void> {
    photoBackupBusy.set(true); photoBackupError.set('');
    try { if (await runUnlocked(async () => { await refreshNativePolicy(true); return resumePhotoBackup(); })) { manuallyPaused = false; await refreshPhotoBackup(); void runDiscoveryScheduler(); } }
    catch (cause) { await refreshPhotoBackup(); reportFailure(cause, 'Could not resume photo backup. Try again.'); }
    finally { photoBackupBusy.set(false); }
}
export async function retryPhotoBackupNow(): Promise<void> {
    photoBackupBusy.set(true); photoBackupError.set('');
    try { if (await runUnlocked(async () => { await refreshNativePolicy(true); return retryPhotoBackup(); })) { await refreshPhotoBackup(); if (!manuallyPaused) void runDiscoveryScheduler(); } }
    catch (cause) { reportFailure(cause, 'Could not retry photo backup. Try again.'); }
    finally { photoBackupBusy.set(false); }
}

async function materialize(payload: unknown): Promise<void> {
    const raw = asRecord(payload); const token = boundedText(raw.token, 512); const asset = normalizeAsset(raw.asset);
    if (!token || !asset) return;
    const controller = new AbortController();
    materializations.set(token, controller);
    try {
        const resource = await materializeNativePhotoBackupAsset(asset, controller.signal);
        if (controller.signal.aborted) { await releaseNativePhotoBackupAsset(resource.releaseID); return; }
        if (resource.path) materializedResources.set(token, resource.releaseID);
        await resolvePhotoBackupResource(token, resource.path, resource.path ? '' : 'The photo could not be read.');
    }
    catch (cause) {
        if (controller.signal.aborted) return;
        const message = cause instanceof Error && cause.message.trim() ? cause.message.trim().slice(0, 240) : 'The media could not be read.';
        try { await resolvePhotoBackupResource(token, '', message); }
        catch { release({ token }); }
    }
}

function release(payload: unknown): void {
    const token = boundedText(asRecord(payload).token, 512);
    materializations.get(token)?.abort();
    materializations.delete(token);
    const releaseID = materializedResources.get(token);
    materializedResources.delete(token);
    if (releaseID) void releaseNativePhotoBackupAsset(releaseID).catch(() => photoBackupError.set('Temporary media cleanup failed. Reopen TDrive to retry cleanup.'));
}

async function continueForegroundDiscovery(): Promise<void> {
    await runDiscoveryScheduler();
}

// Kept at the application lifecycle instead of inside the settings surface so
// queued work can resume while that panel is closed.
export function activatePhotoBackup(): () => void {
    if (active) return () => {};
    active = true; documentVisible = document.visibilityState === 'visible'; void refreshPhotoBackup().then(runDiscoveryScheduler);
    const stopBackground = activatePhotoBackupBackground(photoBackupState, (message) => photoBackupError.set(message));
    const stops: RuntimeUnsubscribe[] = [];
    if (runtimeEventsAvailable()) {
        stops.push(onRuntimeEvent('photo-backup:materialize', materialize));
        stops.push(onRuntimeEvent('photo-backup:release', release));
        stops.push(onRuntimeEvent('photo-backup:state', scheduleRefresh));
        stops.push(onRuntimeEvent('android:PhotoBackupMediaChanged', () => { cancelDiscovery(); void continueForegroundDiscovery(); }));
        stops.push(onRuntimeEvent('ios:PhotoBackupMediaChanged', () => { cancelDiscovery(); void continueForegroundDiscovery(); }));
    }
    const iosResume = () => { cancelDiscovery(); void continueForegroundDiscovery(); };
    window.addEventListener('ios:PhotoBackupMediaChanged', iosResume);
    const visibility = () => { documentVisible = document.visibilityState === 'visible'; if (!documentVisible) { cancelDiscovery(); return; } void runDiscoveryScheduler(); };
    document.addEventListener('visibilitychange', visibility);
    const stopDriveWatch = activeDrive.subscribe((drive) => {
        const id = drive?.id ?? null;
        if (id === observedDriveID) return;
        scopeEpoch += 1;
        clearPhotoBackupActivity();
        publish(null);
        photoBackupError.set('');
        cancelDiscovery();
        observedDriveID = id;
        if (id === null) return;
        const epoch = scopeEpoch;
        void refreshPhotoBackup().then(() => { if (epoch === scopeEpoch) void runDiscoveryScheduler(); });
    });
    return () => { scopeEpoch += 1; stopBackground(); active = false; observedDriveID = null; stopDriveWatch(); clearPhotoBackupActivity(); cancelDiscovery(); for (const token of materializations.keys()) release({ token }); document.removeEventListener('visibilitychange', visibility); window.removeEventListener('ios:PhotoBackupMediaChanged', iosResume); if (refreshTimer) { clearTimeout(refreshTimer); refreshTimer = null; } for (const stop of stops) stop(); };
}
