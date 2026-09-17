import { get, writable } from 'svelte/store';
import {
    addPhotoBackupFolder, defaultSettings, enqueuePhotoBackupAssets, getPhotoBackupState,
    pausePhotoBackup, removePhotoBackupSource, resolvePhotoBackupResource, resumePhotoBackup, retryPhotoBackup, setPhotoBackupPolicy,
    runPhotoBackup, savePhotoBackupSettings, type PhotoBackupAsset, type PhotoBackupSettings,
    type PhotoBackupState, upsertPhotoBackupSource,
} from '../../api/photo-backup';
import { onRuntimeEvent, runtimeEventsAvailable, type RuntimeUnsubscribe } from '../../api/runtime';
import { asRecord, boundedText } from '../../api/shared';
import { listNativePhotoBackupAssets, listNativePhotoBackupSources, materializeNativePhotoBackupAsset, nativePhotoBackupAvailable, releaseNativePhotoBackupAsset, requestNativePhotoBackupAccess, nativePhotoBackupPolicy } from './native-adapter';
import { activeDrive } from '../../ui/mobile/mobile-shell-store';
import { activatePhotoBackupBackground } from './background';
import { openEncryptionPasswordModal } from '../modals/encryption-password';
import { isEncryptionPasswordRequired } from '../errors';

export const photoBackupState = writable<PhotoBackupState | null>(null);
export const photoBackupError = writable('');
export const photoBackupBusy = writable(false);
export const photoBackupCandidates = writable<import('../../api/photo-backup').PhotoBackupSource[]>([]);

let active = false;
let refreshTimer: ReturnType<typeof setTimeout> | null = null;
let schedulerRunning = false;
let schedulerEpoch = 0;
let retryTimer: ReturnType<typeof setTimeout> | null = null;
let manuallyPaused = false;
let documentVisible = typeof document === 'undefined' || document.visibilityState === 'visible';
let observedDriveID: number | null = null;
let policySampling = false;
const completedScans = new Set<string>();
const discoveryCursors = new Map<string, string>();
const materializedResources = new Map<string, string>();
const materializations = new Map<string, AbortController>();

export async function refreshPhotoBackup(): Promise<void> {
    try {
        const state = await getPhotoBackupState();
        manuallyPaused = state.manualPaused;
        photoBackupState.set(state); photoBackupError.set('');
    }
    catch { photoBackupError.set('Photo backup is unavailable. Try again.'); }
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

const yieldToForeground = () => new Promise<void>((resolve) => setTimeout(resolve, 0));

async function refreshNativePolicy(force = false): Promise<void> {
    if (policySampling || !active || !documentVisible || (manuallyPaused && !force)) return;
    policySampling = true;
    try {
        const wifi = await nativePhotoBackupPolicy();
        if (wifi !== null && active && documentVisible) await setPhotoBackupPolicy(wifi);
    } finally { policySampling = false; }
}

// A single serial worker gives native staging and the durable enqueue a bounded
// concurrency of one. It yields after every <=128 asset page so a huge library
// does not monopolize the WebView, but continues automatically while active.
async function runDiscoveryScheduler(): Promise<void> {
    if (schedulerRunning || !active || !documentVisible || manuallyPaused || !nativePhotoBackupAvailable()) return;
    schedulerRunning = true;
    const epoch = schedulerEpoch;
    try {
        const current = await getPhotoBackupState();
        manuallyPaused = current.manualPaused;
        if (!current.settings.enabled || epoch !== schedulerEpoch || manuallyPaused) return;
        if (current.encryptionRequired) {
            photoBackupError.set('Unlock encryption to continue photo backup.');
            return;
        }
        await refreshNativePolicy();
        if (epoch !== schedulerEpoch) return;
        // Drain persisted work even when discovery already reached the end.
        await runPhotoBackup();
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
    } catch (cause) {
        if (isEncryptionPasswordRequired(cause)) {
            if (active && epoch === schedulerEpoch) photoBackupError.set('Unlock encryption to continue photo backup.');
            return;
        }
        if (active && epoch === schedulerEpoch) photoBackupError.set('Backup is waiting for media access or a connection.');
        // Native permission and transient provider failures are retried while
        // foregrounded without presenting an endless stream of errors.
        if (active && epoch === schedulerEpoch) retryTimer = setTimeout(() => { retryTimer = null; void runDiscoveryScheduler(); }, 5_000);
    } finally { schedulerRunning = false; }
}

export async function loadPhotoBackupCandidates(): Promise<void> { if (nativePhotoBackupAvailable()) { await requestNativePhotoBackupAccess(); photoBackupCandidates.set(await listNativePhotoBackupSources()); } }
export async function selectPhotoBackupSource(source: import('../../api/photo-backup').PhotoBackupSource): Promise<void> { await upsertPhotoBackupSource(source); await refreshPhotoBackup(); void runDiscoveryScheduler(); }

// An explicit action may open the existing password modal, then runs once. A
// stale state can still surface the backend's stable locked-vault error; in
// that case unlock and retry the original action exactly once.
async function runWithEncryptionUnlock(action: () => Promise<void>): Promise<boolean> {
    if (get(photoBackupState)?.encryptionRequired && !await openEncryptionPasswordModal()) return false;
    try {
        await action();
        return true;
    } catch (cause) {
        if (!isEncryptionPasswordRequired(cause)) throw cause;
        if (!await openEncryptionPasswordModal()) return false;
        await action();
        return true;
    }
}

export async function startPhotoBackup(): Promise<void> {
    manuallyPaused = false; photoBackupBusy.set(true); photoBackupError.set('');
    try {
        if (await runWithEncryptionUnlock(async () => { await refreshNativePolicy(true); await runPhotoBackup(); })) { await refreshPhotoBackup(); void runDiscoveryScheduler(); }
    } catch { photoBackupError.set('Could not start photo backup. Try again.'); }
    finally { photoBackupBusy.set(false); }
}

export async function updatePhotoBackupSettings(settings: PhotoBackupSettings): Promise<void> {
    cancelDiscovery(); photoBackupBusy.set(true); photoBackupError.set('');
    try { photoBackupState.set(await savePhotoBackupSettings({ ...defaultSettings, ...settings })); if (settings.enabled) void runDiscoveryScheduler(); }
    catch { photoBackupError.set('Could not save backup settings. Try again.'); }
    finally { photoBackupBusy.set(false); }
}

export async function choosePhotoBackupFolder(): Promise<void> {
    photoBackupBusy.set(true); try { await addPhotoBackupFolder(); await refreshPhotoBackup(); } catch { photoBackupError.set('Could not add that folder. Try again.'); } finally { photoBackupBusy.set(false); }
}
export async function deletePhotoBackupSource(id: string): Promise<void> { cancelDiscovery(); photoBackupBusy.set(true); try { await removePhotoBackupSource(id); await refreshPhotoBackup(); } catch { photoBackupError.set('Could not remove that source. Try again.'); } finally { photoBackupBusy.set(false); } }
export async function pausePhotoBackupNow(): Promise<void> {
    manuallyPaused = true; cancelDiscovery();
    for (const controller of materializations.values()) controller.abort();
    photoBackupBusy.set(true); photoBackupError.set('');
    try { await pausePhotoBackup(); await refreshPhotoBackup(); }
    catch { await refreshPhotoBackup(); photoBackupError.set('Could not pause photo backup. Try again.'); }
    finally { photoBackupBusy.set(false); }
}
export async function resumePhotoBackupNow(): Promise<void> {
    photoBackupBusy.set(true); photoBackupError.set('');
    try { if (await runWithEncryptionUnlock(async () => { await refreshNativePolicy(true); await resumePhotoBackup(); })) { manuallyPaused = false; await refreshPhotoBackup(); void runDiscoveryScheduler(); } }
    catch { await refreshPhotoBackup(); photoBackupError.set('Could not resume photo backup. Try again.'); }
    finally { photoBackupBusy.set(false); }
}
export async function retryPhotoBackupNow(): Promise<void> {
    photoBackupBusy.set(true); photoBackupError.set('');
    try { if (await runWithEncryptionUnlock(async () => { await refreshNativePolicy(true); await retryPhotoBackup(); })) { await refreshPhotoBackup(); if (!manuallyPaused) void runDiscoveryScheduler(); } }
    catch { photoBackupError.set('Could not retry photo backup. Try again.'); }
    finally { photoBackupBusy.set(false); }
}

function parseAsset(value: unknown): PhotoBackupAsset | null {
    const raw = asRecord(value); const id = boundedText(raw.id, 512); const version = boundedText(raw.version, 256); const mediaType = raw.media_type === 'video' ? 'video' : raw.media_type === 'photo' ? 'photo' : null;
    return id && version && mediaType ? { id, version, name: boundedText(raw.name, 512), mediaType, modifiedAt: Number(raw.modified_at) || 0, size: Number(raw.size) || 0, resourceId: boundedText(raw.resource_id, 512) || undefined } : null;
}

async function materialize(payload: unknown): Promise<void> {
    const raw = asRecord(payload); const token = boundedText(raw.token, 512); const asset = parseAsset(raw.asset);
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
    const stopDriveWatch = activeDrive.subscribe((drive) => { const id = drive?.id ?? null; if (id !== observedDriveID) { cancelDiscovery(); observedDriveID = id; if (id !== null) void runDiscoveryScheduler(); } });
    return () => { stopBackground(); active = false; observedDriveID = null; stopDriveWatch(); cancelDiscovery(); for (const token of materializations.keys()) release({ token }); document.removeEventListener('visibilitychange', visibility); window.removeEventListener('ios:PhotoBackupMediaChanged', iosResume); if (refreshTimer) { clearTimeout(refreshTimer); refreshTimer = null; } for (const stop of stops) stop(); };
}
