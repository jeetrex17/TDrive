import type { Readable } from 'svelte/store';
import type { PhotoBackupState } from '../../api/photo-backup';
import { setPhotoBackupBackgroundLease } from '../../api/photo-backup';
import { canRunInBackground, setPhotoBackupBackgroundDemand, type ForegroundSummary } from '../android-foreground';
import { onRuntimeEvent, runtimeEventsAvailable, type RuntimeUnsubscribe } from '../../api/runtime';
import { setIOSPhotoBackupBackground } from './native-adapter';

const NOTICE = { title: 'Backing up photos', text: 'Finishing the current item', progress: -1 };

export function activatePhotoBackupBackground(
    state: Readable<PhotoBackupState | null>,
    report: (message: string) => void,
): () => void {
    let desired = false, applied = false, disposed = false, generation = 0;
    let work = Promise.resolve();
    // What the run had done when it took the process, so the line left behind
    // counts this backup rather than every backup the drive has ever done.
    let latest: PhotoBackupState | null = null;
    let completeAtStart = 0;
    let failedAtStart = 0;

    /**
     * The one line the user finds afterwards.
     *
     * A backup that ran while the phone was in a pocket ends with its ongoing
     * notification simply disappearing, which says nothing about whether the
     * photos are safe. This says so, once. Whether the user was there to watch
     * it is not decided here: the host layer drops a summary raised while the
     * app is on screen, and one place owning that question is the reason it
     * cannot be answered two different ways.
     */
    const summary = (): ForegroundSummary | undefined => {
        const done = Math.max(0, (latest?.status.complete ?? 0) - completeAtStart);
        const failed = Math.max(0, (latest?.status.failed ?? 0) - failedAtStart);
        if (done === 0 && failed === 0) return undefined;
        if (failed > 0) {
            return {
                outcome: 'stopped',
                title: 'Photo backup needs attention',
                text: failed === 1 ? '1 item could not be backed up' : `${failed} items could not be backed up`,
            };
        }
        return {
            outcome: 'complete',
            title: 'Photos backed up',
            text: done === 1 ? '1 item added to your drive' : `${done} items added to your drive`,
        };
    };
    const acquireNative = async (): Promise<boolean> => {
        if (canRunInBackground()) { await setPhotoBackupBackgroundDemand(NOTICE); return true; }
        return setIOSPhotoBackupBackground(true);
    };
    const releaseNative = async (): Promise<void> => {
        if (canRunInBackground()) await setPhotoBackupBackgroundDemand(null, summary());
        else await setIOSPhotoBackupBackground(false);
    };
    const reconcile = (): void => {
        const ticket = ++generation;
        // Acquire only while visible (Android rejects background starts), then
        // keep an acquired lease across the actual visibility transition.
        const target = desired && (applied || document.visibilityState === 'visible');
        work = work.catch(() => undefined).then(async () => {
            if (target === applied) return;
            if (!target) {
                try {
                    await setPhotoBackupBackgroundLease(false);
                    await releaseNative();
                    applied = false;
                } catch {
                    report('Background backup cleanup could not finish. It will retry when backup state changes.');
                }
                return;
            }
            let acquired = false;
            try {
                acquired = await acquireNative();
                if (!acquired) {
                    report('Background backup is unavailable on this build. Keep TDrive open to continue.');
                    return;
                }
                if (disposed || ticket !== generation || !desired || document.visibilityState !== 'visible') {
                    await releaseNative(); return;
                }
                await setPhotoBackupBackgroundLease(true);
                if (disposed || ticket !== generation || !desired) {
                    await setPhotoBackupBackgroundLease(false);
                    await releaseNative(); return;
                }
                applied = true;
                completeAtStart = latest?.status.complete ?? 0;
                failedAtStart = latest?.status.failed ?? 0;
            } catch {
                if (acquired) await releaseNative().catch(() => undefined);
                applied = false;
                report('Background backup could not start. Keep TDrive open to continue.');
            }
        });
    };
    const unsubscribe = state.subscribe((value) => {
        latest = value;
        desired = Boolean((value?.platform === 'android' || value?.platform === 'ios')
            && value.settings.enabled && !value.manualPaused
            && (value.status.uploading > 0 || value.status.phase === 'uploading'));
        reconcile();
    });
    const expire = () => { desired = false; reconcile(); };
    const visible = () => reconcile();
    const stops: RuntimeUnsubscribe[] = [];
    if (runtimeEventsAvailable()) stops.push(onRuntimeEvent('android:BackgroundTransferExpired', expire));
    window.addEventListener('ios:PhotoBackupBackgroundExpired', expire);
    document.addEventListener('visibilitychange', visible);
    return () => {
        unsubscribe();
        for (const stop of stops) stop();
        window.removeEventListener('ios:PhotoBackupBackgroundExpired', expire);
        document.removeEventListener('visibilitychange', visible);
        disposed = true;
        desired = false;
        reconcile();
    };
}
