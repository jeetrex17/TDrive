// Update orchestration: hydrate the version banner, subscribe to backend
// update-state events, schedule silent checks, and apply the auto-download /
// announcement policy. Mechanism lives in Go (backend/updater); this module
// owns only the policy and the glue to the notification + panel surfaces.

import { get } from 'svelte/store';
import {
    cancelUpdateDownload as requestCancelUpdateDownload,
    checkForUpdate as requestUpdateCheck,
    downloadUpdate as requestDownloadUpdate,
    getAppVersion,
    getMountStatus,
    getUpdateState,
    installUpdateAndRestart,
    onRuntimeEvent,
    onUpdateState,
    openUpdatePage,
} from '../api';
import { state } from '../state';
import { notify } from './notifications';
import { humanizeBackendError } from './errors';
import { isVersionSkipped, type UpdateState } from '../ui/updates/update-model';
import {
    appVersionInfo,
    requestUpdatesPanel,
    updatePrefs,
    updateState,
} from '../ui/updates/update-store';

// Silent background cadence. The first check trails dashboard bring-up so it
// never competes with the initial Telegram sync; then once a day, because a
// drive app is commonly left running for days at a time.
const FIRST_CHECK_DELAY_MS = 10_000;
const CHECK_INTERVAL_MS = 24 * 60 * 60 * 1000;

let started = false;
let firstCheckTimer: ReturnType<typeof setTimeout> | null = null;
let intervalTimer: ReturnType<typeof setInterval> | null = null;
let stopUpdateState: (() => void) | null = null;
let stopOpenUpdates: (() => void) | null = null;

// Versions we've already acted on, so a phase that re-emits (or a failed
// download dropping back to "available") can't spam toasts or retry forever.
const announced = new Set<string>();
const autoDownloaded = new Set<string>();

export function activateUpdates(): () => void {
    if (started) return teardownUpdates;
    started = true;

    stopUpdateState = onUpdateState(applyState);
    // Native "Check for Updates…" (macOS menu) and any other explicit entry
    // point routes through the same panel action.
    stopOpenUpdates = onRuntimeEvent('updates:open', () => {
        void openUpdatesUI();
    });

    void hydrateVersion();
    void getUpdateState()
        .then(applyState)
        .catch((err) => console.warn('GetUpdateState failed:', err));

    scheduleChecks();
    return teardownUpdates;
}

export function teardownUpdates(): void {
    if (firstCheckTimer) clearTimeout(firstCheckTimer);
    if (intervalTimer) clearInterval(intervalTimer);
    stopUpdateState?.();
    stopOpenUpdates?.();
    stopUpdateState = null;
    stopOpenUpdates = null;
    firstCheckTimer = null;
    intervalTimer = null;
    started = false;
}

async function hydrateVersion(): Promise<void> {
    try {
        const info = await getAppVersion();
        appVersionInfo.set(info);
    } catch (err) {
        console.warn('AppVersion failed:', err);
    }
}

function scheduleChecks(): void {
    const info = get(appVersionInfo);
    if (info?.devBuild) return; // updater is disabled for local builds
    firstCheckTimer = setTimeout(() => {
        void checkForUpdates();
    }, FIRST_CHECK_DELAY_MS);
    intervalTimer = setInterval(() => {
        void checkForUpdates();
    }, CHECK_INTERVAL_MS);
}

// applyState is the single reducer for every backend snapshot, whether it
// arrived as an event or as a binding return value. It must stay idempotent.
function applyState(next: UpdateState | null | undefined): void {
    if (!next || typeof next.phase !== 'string') return;
    updateState.set(next);
    maybeAutoDownload(next);
    maybeAnnounce(next);
}

function maybeAutoDownload(next: UpdateState): void {
    if (next.phase !== 'available' || !next.installable || !next.latest) return;
    const prefs = get(updatePrefs);
    if (!prefs.autoDownload) return;
    if (isVersionSkipped(next.latest.version, prefs.skippedVersion)) return;
    // One automatic attempt per version: a failed download returns to
    // "available" with an error, and we must not immediately re-fire.
    if (autoDownloaded.has(next.latest.version)) return;
    // Don't steal Telegram bandwidth from an active transfer; the next
    // scheduled check picks it up once the transfer finishes.
    if (state.transferActivity.upload || state.transferActivity.download) return;
    autoDownloaded.add(next.latest.version);
    void requestDownloadUpdate().catch((err) => console.warn('auto-download failed:', err));
}

function maybeAnnounce(next: UpdateState): void {
    if (!next.latest) return;
    if (next.phase !== 'available' && next.phase !== 'ready') return;
    const prefs = get(updatePrefs);
    if (isVersionSkipped(next.latest.version, prefs.skippedVersion)) return;
    // Announce a given version once. "ready" supersedes "available" so a user
    // who has auto-download on sees a single actionable toast, not two.
    const key = `${next.latest.version}:${next.phase}`;
    if (announced.has(key) || announced.has(`${next.latest.version}:ready`)) return;
    announced.add(key);

    if (next.phase === 'ready') {
        notify({
            level: 'info',
            title: `TDrive ${next.latest.version} is ready`,
            body: 'Restart to update from the account menu.',
        });
    } else {
        notify({
            level: 'info',
            title: `TDrive ${next.latest.version} is available`,
            body: 'Open the account menu to update.',
        });
    }
}

// checkForUpdates runs a check. Explicit checks (menu, button) confirm the
// "up to date" outcome with a toast; scheduled checks stay silent unless they
// surface a new version through applyState.
export async function checkForUpdates(options: { explicit?: boolean } = {}): Promise<void> {
    try {
        const result = await requestUpdateCheck();
        applyState(result);
        if (options.explicit) {
            if (result.error && result.errorStage === 'check') {
                notify({ level: 'error', title: 'Update check failed', body: result.error });
            } else if (result.phase === 'up_to_date') {
                notify({ level: 'success', title: 'TDrive is up to date' });
            } else if (result.phase === 'disabled') {
                notify({ level: 'info', title: 'Updates are off for this build' });
            }
        }
    } catch (err) {
        if (options.explicit) {
            notify({ level: 'error', title: 'Update check failed', body: humanizeBackendError(err) });
        } else {
            console.warn('update check failed:', err);
        }
    }
}

export async function downloadUpdate(): Promise<void> {
    try {
        await requestDownloadUpdate();
    } catch (err) {
        notify({ level: 'error', title: 'Could not start the download', body: humanizeBackendError(err) });
    }
}

export function cancelUpdateDownload(): void {
    void requestCancelUpdateDownload().catch((err) => console.warn('cancel download failed:', err));
}

export async function installUpdate(): Promise<void> {
    try {
        await installUpdateAndRestart();
    } catch (err) {
        notify({
            level: 'error',
            title: 'Could not install the update',
            body: humanizeBackendError(err),
        });
    }
}

export function openReleasePage(): void {
    void openUpdatePage().catch((err) => console.warn('open release page failed:', err));
}

// getRestartRisks lists, in plain language, what a restart-to-update will
// interrupt right now: in-flight transfers, a mounted drive, a remembered
// vault password. The panel shows these before the final confirm.
export async function getRestartRisks(): Promise<string[]> {
    const risks: string[] = [];
    if (state.transferActivity.upload) {
        risks.push('An upload is in progress and will be cancelled.');
    }
    if (state.transferActivity.download) {
        risks.push('A download is in progress and will be cancelled.');
    }
    try {
        const mount = await getMountStatus();
        if (mount?.mounted) {
            risks.push('The mounted drive will be ejected first.');
        }
    } catch (err) {
        console.warn('MountStatus during restart check failed:', err);
    }
    if (state.encryption?.passwordRemembered) {
        risks.push("You'll re-enter your encryption password after restart.");
    }
    return risks;
}

// openUpdatesUI is the shared entry point for explicit "check for updates"
// gestures: reveal the panel where possible and always run a check so the
// action answers even from the login screen, where the panel isn't mounted.
export async function openUpdatesUI(): Promise<void> {
    requestUpdatesPanel();
    await checkForUpdates({ explicit: true });
}
