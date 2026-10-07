import { get, writable } from 'svelte/store';
import {
    addPhotoBackupFolder, commitPhotoBackupScanPage, defaultSettings, getPhotoBackupState, normalizeAsset,
    pausePhotoBackup, removePhotoBackupSource, resolvePhotoBackupResource, resumePhotoBackup, retryPhotoBackup, setPhotoBackupPolicy,
    resetPhotoBackupScan, runPhotoBackup, savePhotoBackupSettings, type PhotoBackupAsset, type PhotoBackupSettings,
    type PhotoBackupState, upsertPhotoBackupSource,
} from '../../api/photo-backup';
import { OperationFailure, requireOperationSuccess } from '../../api/operation';
import { onRuntimeEvent, runtimeEventsAvailable, type RuntimeUnsubscribe } from '../../api/runtime';
import { asRecord, boundedText } from '../../api/shared';
import type { OperationError, OperationResult } from '../../types';
import { listNativePhotoBackupAssets, materializeNativePhotoBackupAsset, nativePhotoBackupAvailable, nativePhotoBackupFolderPicking, pickNativePhotoBackupFolder, releaseNativePhotoBackupAsset, requestNativePhotoBackupAccess, nativePhotoBackupPolicy } from './native-adapter';
import { activeDrive } from '../../ui/mobile/mobile-shell-store';
import { activatePhotoBackupBackground } from './background';
import { requireEncryptionPassword } from '../encryption';
import { humanizeBackendError } from '../errors';
import { clearPhotoBackupActivity, syncPhotoBackupActivity } from './activity';

export const photoBackupState = writable<PhotoBackupState | null>(null);
export const photoBackupError = writable('');
export const photoBackupBusy = writable(false);
// What the device says about the media grant. It lives here rather than in the
// backend state because only the host can see an Android permission, and a
// partial grant is the one thing that explains a folder backing up less than
// the person can see in it.
export const photoBackupAccessNote = writable('');

const UNLOCK_MESSAGE = 'Unlock encryption to continue photo backup.';
/** How long to sit on a device-side wait (Wi-Fi, policy) before asking again. */
const POLICY_RECHECK_MS = 30_000;
/** How long to sit on a native access or provider failure before trying again. */
const ACCESS_RETRY_MS = 5_000;

let active = false;
let refreshTimer: ReturnType<typeof setTimeout> | null = null;
let schedulerRunning = false;
// A resume, source change or media event can arrive while a canceled native
// page is still settling. Keep that request until the serial worker exits.
let schedulerRestartRequested = false;
let schedulerEpoch = 0;
let scanPassedStart = false;
let scanNeedsReconcile = false;
// A backend reply belongs to the account and drive that requested it. Changing
// either scope invalidates replies already in flight so an old filename cannot
// reappear in the bell after a switch.
let scopeEpoch = 0;
let retryTimer: ReturnType<typeof setTimeout> | null = null;
let manuallyPaused = false;
let documentVisible = typeof document === 'undefined' || document.visibilityState === 'visible';
let observedDriveID: number | null = null;
let policySampling = false;
let policyTimer: ReturnType<typeof setTimeout> | null = null;
const completedScans = new Set<string>();
interface NativeStage {
    controller: AbortController;
    released: Promise<void>;
    requestRelease: () => void;
    bytes: number;
    cleaned: Promise<void>;
    markCleaned: () => void;
}

const materializations = new Map<string, NativeStage>();
let nativeStageTail: Promise<void> = Promise.resolve();
const nativeStageLeases = new Set<NativeStage>();
const NATIVE_STAGE_MAX_BYTES = 4 * 1024 ** 3;

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
    completedScans.clear();
    if (retryTimer) { clearTimeout(retryTimer); retryTimer = null; }
}

function retryDiscoveryAfter(delay: number, epoch: number): void {
    if (!active || epoch !== schedulerEpoch) return;
    retryTimer = setTimeout(() => { retryTimer = null; void runDiscoveryScheduler(); }, delay);
}

const yieldToForeground = () => new Promise<void>((resolve) => setTimeout(resolve, 0));

async function refreshNativePolicy(force = false): Promise<boolean> {
    if (policySampling || !active || !documentVisible || (manuallyPaused && !force)) return false;
    const epoch = scopeEpoch;
    policySampling = true;
    try {
        const wifi = await nativePhotoBackupPolicy();
        if (wifi === null || epoch !== scopeEpoch || !active || !documentVisible || (manuallyPaused && !force)) return false;
        await setPhotoBackupPolicy(wifi);
        return wifi && epoch === scopeEpoch && active && documentVisible;
    } finally { policySampling = false; }
}

// The backend expires connectivity observations after two minutes. Discovery
// can finish long before an upload, so it cannot own report freshness.
function schedulePolicyRefresh(): void {
    if (policyTimer || !active || !documentVisible) return;
    const epoch = scopeEpoch;
    policyTimer = setTimeout(async () => {
        policyTimer = null;
        try {
            const state = backendState;
            if (active && documentVisible && state?.platform === 'android' && state.settings.enabled && state.settings.wifiOnly && !manuallyPaused) {
                const permitted = await refreshNativePolicy();
                if (permitted && epoch === scopeEpoch && !manuallyPaused && !state.encryptionRequired && state.status.uploading === 0 && (state.status.pending > 0 || state.status.failed > 0)) void runDiscoveryScheduler();
            }
        } catch {
            if (epoch === scopeEpoch && active && documentVisible) photoBackupError.set('Could not check Wi-Fi. Backup will retry the connectivity check.');
        }
        finally { if (epoch === scopeEpoch) schedulePolicyRefresh(); }
    }, POLICY_RECHECK_MS);
}

function stopPolicyRefresh(): void {
    if (policyTimer) clearTimeout(policyTimer);
    policyTimer = null;
}

/** Shown when the backend declines to run: its own words, or the unlock hint for a locked vault. */
function refusalMessage(error: OperationError): string {
    return error.code === 'encryption_password_required' ? UNLOCK_MESSAGE : humanizeBackendError(error);
}

// A single serial worker gives native staging and the durable enqueue a bounded
// concurrency of one. It yields after every <=128 asset page so a huge library
// does not monopolize the WebView, but continues automatically while active.
async function runDiscoveryScheduler(): Promise<void> {
    if (!active || !documentVisible || manuallyPaused || !nativePhotoBackupAvailable()) return;
    if (schedulerRunning) { schedulerRestartRequested = true; return; }
    schedulerRunning = true;
    scanPassedStart = false;
    const epoch = schedulerEpoch;
    const currentScope = scopeEpoch;
    let discoveryCompleted = false;
    try {
        const current = await getPhotoBackupState();
        if (currentScope !== scopeEpoch) return;
        manuallyPaused = current.manualPaused;
        if (!current.settings.enabled || epoch !== schedulerEpoch || manuallyPaused) return;
        if (current.encryptionRequired) {
            photoBackupError.set(current.status.message || UNLOCK_MESSAGE);
            return;
        }
        await refreshNativePolicy();
        if (epoch !== schedulerEpoch) return;
        // Drain persisted work even when discovery already reached the end. A
        // refusal is a state to show rather than an error to retry into: a
        // locked vault waits for the user, a Wi-Fi wait for the device, and
        // the device is asked again after a while.
        const drained = await runPhotoBackup();
        if (epoch !== schedulerEpoch || currentScope !== scopeEpoch || !active || !documentVisible) return;
        if (!drained.ok) {
            photoBackupError.set(refusalMessage(drained.error));
            if (drained.error.code === 'operation_failed') retryDiscoveryAfter(POLICY_RECHECK_MS, epoch);
            return;
        }
        setDiscovering(true);
        for (const source of current.sources.filter((item) => item.enabled)) {
            if (completedScans.has(source.id)) continue;
            let cursor = source.scanCursor ?? '';
            if (source.scanComplete) {
                if (epoch !== schedulerEpoch || currentScope !== scopeEpoch || !active || !documentVisible) return;
                await resetPhotoBackupScan(source.id);
                if (epoch !== schedulerEpoch) return;
                cursor = '';
            }
            if (cursor) scanPassedStart = true;
            while (active && epoch === schedulerEpoch) {
                const page = await listNativePhotoBackupAssets(source.id, cursor);
                if (epoch !== schedulerEpoch) return;
                const next = page.nextCursor;
                if (next && next === cursor) throw new Error('Photo library cursor did not advance.');
                await commitPhotoBackupScanPage(source.id, cursor, next, page.assets);
                if (epoch !== schedulerEpoch) return;
                cursor = next;
                scanPassedStart = true;
                await runPhotoBackup();
                if (!next) { completedScans.add(source.id); break; }
                await yieldToForeground();
            }
        }
        discoveryCompleted = true;
        await refreshPhotoBackup();
    } catch {
        if (active && epoch === schedulerEpoch) photoBackupError.set('Backup is waiting for media access or a connection.');
        // Native permission and transient provider failures are retried while
        // foregrounded without presenting an endless stream of errors.
        retryDiscoveryAfter(ACCESS_RETRY_MS, epoch);
    } finally {
        setDiscovering(false);
        schedulerRunning = false;
        scanPassedStart = false;
        if (discoveryCompleted && scanNeedsReconcile) {
            scanNeedsReconcile = false;
            completedScans.clear();
            schedulerRestartRequested = true;
        }
        const restart = schedulerRestartRequested;
        schedulerRestartRequested = false;
        if (restart) void runDiscoveryScheduler();
    }
}

/**
 * Whether a folder can be added on this host. Desktop always can, through the
 * backend's own dialog; a phone only where the host offers a picker, which is
 * Android. iOS has no folder to offer -- the photo library is the unit there,
 * and the sandbox has no user-visible tree to watch -- so the button is absent
 * rather than present and refusing.
 */
export function photoBackupFolderPicking(): boolean { return !nativePhotoBackupAvailable() || nativePhotoBackupFolderPicking(); }

// An explicit action may open first-time encryption setup or the existing
// password modal, then runs once. The snapshot can be stale, so the backend's
// stable locked-vault code is the second chance and is retried exactly once.
// Resolves false when the user dismissed the prompt, which is not a failure.
async function runUnlocked(action: () => Promise<OperationResult>): Promise<boolean> {
    if (get(photoBackupState)?.encryptionRequired && !await requireEncryptionPassword()) return false;
    let result = await action();
    if (!result.ok && result.error.code === 'encryption_password_required') {
        if (!await requireEncryptionPassword()) return false;
        result = await action();
    }
    if (result.ok) return true;
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
    try {
        const enabling = settings.enabled && get(photoBackupState)?.settings.enabled !== true;
        if (enabling && get(activeDrive)?.kind === 'shared') {
            photoBackupError.set('Encrypted photo backup is available only in My Drive.');
            return;
        }
        if (enabling && !await requireEncryptionPassword()) return;
        publish(await savePhotoBackupSettings({ ...defaultSettings, ...settings, encrypt: true }));
        if (settings.enabled) void runDiscoveryScheduler();
    }
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
            // A phone reads a watched folder through its media library, so the
            // folder and the grant are two permissions and both are needed.
            // Asked for here because adding a folder is the moment it matters.
            const access = await requestNativePhotoBackupAccess();
            photoBackupAccessNote.set(access.status === 'limited' || access.status === 'denied' ? access.detail : '');
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

async function runMaterialization(token: string, asset: PhotoBackupAsset, stage: NativeStage): Promise<void> {
    let releaseID = '';
    try {
        if (stage.controller.signal.aborted) return;
        if (stage.bytes > NATIVE_STAGE_MAX_BYTES) throw new Error('The media exceeds the 4 GiB staging limit.');
        while (nativeStageLeases.size >= 2 || [...nativeStageLeases].reduce((total, lease) => total + lease.bytes, 0) + stage.bytes > NATIVE_STAGE_MAX_BYTES) {
            await Promise.race([stage.released, ...[...nativeStageLeases].map((lease) => lease.cleaned)]);
            if (stage.controller.signal.aborted) return;
        }
        nativeStageLeases.add(stage);
        const resource = await materializeNativePhotoBackupAsset(asset, stage.controller.signal);
        releaseID = resource.releaseID;
        if (stage.controller.signal.aborted) return;
        await resolvePhotoBackupResource(token, resource.path, resource.path ? '' : 'The photo could not be read.');
    }
    catch (cause) {
        if (!stage.controller.signal.aborted) {
            const message = cause instanceof Error && cause.message.trim() ? cause.message.trim().slice(0, 240) : 'The media could not be read.';
            try { await resolvePhotoBackupResource(token, '', message); }
            catch { stage.requestRelease(); }
        }
    }
    finally {
        // Only acquisition is serialized. A completed snapshot can upload
        // while the next copy is prepared, with count/byte leases retained
        // until native cleanup actually finishes.
        void cleanupMaterialization(token, stage, releaseID);
    }
}

async function cleanupMaterialization(token: string, stage: NativeStage, releaseID: string): Promise<void> {
    try {
        await stage.released;
        if (releaseID) {
            try { await releaseNativePhotoBackupAsset(releaseID); }
            catch { photoBackupError.set('Temporary media cleanup failed. Reopen TDrive to retry cleanup.'); }
        }
    } finally {
        nativeStageLeases.delete(stage);
        stage.markCleaned();
        materializations.delete(token);
    }
}

function materialize(payload: unknown): void {
    const raw = asRecord(payload); const token = boundedText(raw.token, 512); const asset = normalizeAsset(raw.asset);
    if (!token || !asset || materializations.has(token)) return;
    const controller = new AbortController();
    let requestRelease = () => {};
    const released = new Promise<void>((resolve) => { requestRelease = resolve; });
    let markCleaned = () => {};
    const cleaned = new Promise<void>((resolve) => { markCleaned = resolve; });
    const stage = { controller, released, requestRelease, bytes: asset.size, cleaned, markCleaned };
    materializations.set(token, stage);
    const work = nativeStageTail.then(() => runMaterialization(token, asset, stage));
    nativeStageTail = work.catch(() => undefined);
}

function release(payload: unknown): void {
    const token = boundedText(asRecord(payload).token, 512);
    const stage = materializations.get(token);
    stage?.controller.abort();
    stage?.requestRelease();
}

async function continueForegroundDiscovery(): Promise<void> {
    await runDiscoveryScheduler();
}

// Kept at the application lifecycle instead of inside the settings surface so
// queued work can resume while that panel is closed.
export function activatePhotoBackup(): () => void {
    if (active) return () => {};
    active = true; documentVisible = document.visibilityState === 'visible'; void refreshPhotoBackup().then(runDiscoveryScheduler);
    schedulePolicyRefresh();
    const stopBackground = activatePhotoBackupBackground(photoBackupState, (message) => photoBackupError.set(message));
    const stops: RuntimeUnsubscribe[] = [];
    if (runtimeEventsAvailable()) {
        stops.push(onRuntimeEvent('photo-backup:materialize', materialize));
        stops.push(onRuntimeEvent('photo-backup:release', release));
        stops.push(onRuntimeEvent('photo-backup:state', scheduleRefresh));
        stops.push(onRuntimeEvent('android:PhotoBackupMediaChanged', () => { if (schedulerRunning && scanPassedStart) scanNeedsReconcile = true; cancelDiscovery(); void continueForegroundDiscovery(); }));
        stops.push(onRuntimeEvent('ios:PhotoBackupMediaChanged', () => { cancelDiscovery(); void continueForegroundDiscovery(); }));
    }
    const iosResume = () => { cancelDiscovery(); void continueForegroundDiscovery(); };
    window.addEventListener('ios:PhotoBackupMediaChanged', iosResume);
    const visibility = () => { documentVisible = document.visibilityState === 'visible'; if (!documentVisible) { stopPolicyRefresh(); cancelDiscovery(); return; } schedulePolicyRefresh(); void runDiscoveryScheduler(); };
    document.addEventListener('visibilitychange', visibility);
    const stopDriveWatch = activeDrive.subscribe((drive) => {
        const id = drive?.id ?? null;
        if (id === observedDriveID) return;
        scopeEpoch += 1;
        stopPolicyRefresh();
        schedulePolicyRefresh();
        clearPhotoBackupActivity();
        publish(null);
        photoBackupError.set('');
        cancelDiscovery();
        scanNeedsReconcile = false;
        observedDriveID = id;
        if (id === null) return;
        const epoch = scopeEpoch;
        void refreshPhotoBackup().then(() => { if (epoch === scopeEpoch) void runDiscoveryScheduler(); });
    });
    return () => { scopeEpoch += 1; stopBackground(); active = false; stopPolicyRefresh(); observedDriveID = null; stopDriveWatch(); clearPhotoBackupActivity(); cancelDiscovery(); scanNeedsReconcile = false; for (const token of materializations.keys()) release({ token }); document.removeEventListener('visibilitychange', visibility); window.removeEventListener('ios:PhotoBackupMediaChanged', iosResume); if (refreshTimer) { clearTimeout(refreshTimer); refreshTimer = null; } for (const stop of stops) stop(); };
}
