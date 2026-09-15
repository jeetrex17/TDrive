/*
 * Connectivity.
 *
 * Losing the network used to show up as a single failed operation and then
 * silence, which reads as the app being broken rather than the link being
 * down. A sticky toast keeps the reason on screen for as long as it is true.
 *
 * navigator.onLine is only trustworthy in the negative: false means there is
 * no link, while true only means one exists, not that Telegram is reachable.
 * That is enough for this, because the claim being made is "you are offline",
 * never "everything is fine".
 */

import { appActions } from './app-actions';
import { dismissNotification, notify } from './notifications';

const OFFLINE_TOAST_ID = 'connectivity-offline';

let installed = false;

/** isOffline reports a known-missing network link. */
export function isOffline(): boolean {
    return typeof navigator !== 'undefined' && navigator.onLine === false;
}

export function reportOffline(): void {
    notify({
        id: OFFLINE_TOAST_ID,
        level: 'warning',
        title: "You're offline",
        body: 'TDrive cannot reach Telegram. Anything already downloaded stays available.',
        sticky: true,
    });
}

export function reportOnline(): void {
    // Replacing the offline toast in place, rather than dismissing and adding
    // one, keeps the stack from jumping while the two swap over.
    notify({
        id: OFFLINE_TOAST_ID,
        level: 'success',
        title: 'Back online',
        body: 'This folder may be out of date.',
        durationMs: 6000,
        action: {
            label: 'Refresh',
            run: () => {
                dismissNotification(OFFLINE_TOAST_ID);
                void appActions().triggerRefresh();
            },
        },
    });
}

/**
 * activateConnectivityWatch reports link changes for as long as it is active.
 * Starting while already offline reports it immediately; starting online says
 * nothing, because there is no news in a working connection.
 */
export function activateConnectivityWatch(): () => void {
    if (installed) return deactivateConnectivityWatch;
    installed = true;
    if (isOffline()) reportOffline();
    window.addEventListener('offline', reportOffline);
    window.addEventListener('online', reportOnline);
    return deactivateConnectivityWatch;
}

function deactivateConnectivityWatch(): void {
    if (!installed) return;
    installed = false;
    window.removeEventListener('offline', reportOffline);
    window.removeEventListener('online', reportOnline);
    dismissNotification(OFFLINE_TOAST_ID);
}
