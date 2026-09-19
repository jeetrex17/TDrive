/**
 * What the buttons on TDrive's system notifications actually do.
 *
 * The host knows nothing about them: it carries an opaque id out to the shade
 * and hands the same id back when the button is pressed. Everything that gives
 * one meaning lives here, so adding a button is a change to one file rather
 * than a change to the Java, the bridge and the page at once.
 *
 * Ids are namespaced by the thing they act on and parsed, never matched by
 * prefix alone: "backup:pause" and a retry key that happens to start with the
 * same word must not be able to trigger each other.
 */

import { get } from 'svelte/store';
import { onRuntimeEvent, runtimeEventsAvailable, type RuntimeUnsubscribe } from '../api/runtime';
import { asRecord, boundedText } from '../api/shared';
import { cancelTransfersInDirection } from './notif-bell';
import { downloadRetryFor } from './transfers';
import { pausePhotoBackupNow } from './photo-backup/controller';
import { historyEvents } from '../ui/notifications/notif-store';
import { activeTab } from '../ui/mobile/mobile-shell-store';
import type { NotificationAction } from './android-foreground';

/** Pauses the photo backup queue, the way the panel's own Pause does. */
export const PAUSE_BACKUP_ACTION: NotificationAction = { id: 'backup:pause', label: 'Pause' };

/** Stops whatever the user started in one direction. */
export function stopTransfersAction(direction: 'up' | 'down'): NotificationAction {
    return { id: `transfers:stop:${direction}`, label: 'Stop' };
}

/**
 * Re-queues one failed download, or nothing where there is no second chance to
 * offer: an upload's source path is gone by the time the row exists, so the
 * only honest button for it is none.
 */
export function retryTransferAction(transferId: string): NotificationAction {
    return { id: `transfers:retry:${transferId}`, label: 'Retry' };
}

/** Runs the action an id names, and reports whether it named anything. */
function dispatch(id: string): boolean {
    if (id === PAUSE_BACKUP_ACTION.id) {
        void pausePhotoBackupNow();
        return true;
    }
    const stop = /^transfers:stop:(up|down)$/.exec(id);
    if (stop) {
        cancelTransfersInDirection(stop[1] as 'up' | 'down');
        return true;
    }
    const retry = id.startsWith('transfers:retry:') ? id.slice('transfers:retry:'.length) : '';
    if (retry) {
        // The row is the source of truth for what a retry means, and it may
        // have moved on since the notification was posted -- finished by
        // another attempt, or cleared. A button for work that is no longer
        // there does nothing rather than guessing.
        const transfer = get(historyEvents)
            .find((event) => event.kind === 'transfer' && event.id === retry);
        const run = transfer?.kind === 'transfer' ? downloadRetryFor(transfer) : undefined;
        run?.();
        return true;
    }
    return false;
}

/**
 * Listens for pressed buttons and for taps that asked to open a screen.
 *
 * Inert off Android, where no host emits either event. Returns the teardown
 * the shell owns, so the listeners live exactly as long as the phone shell.
 */
export function activateNotificationActions(): () => void {
    if (!runtimeEventsAvailable()) return () => {};
    const stops: RuntimeUnsubscribe[] = [
        onRuntimeEvent('android:NotificationAction', (payload) => {
            const id = boundedText(asRecord(payload).id, 128);
            if (id) dispatch(id);
        }),
        // A notification is about work, and work lives on the Transfers tab.
        // The host sends the word rather than moving the app itself: where the
        // user lands is the page's decision, and the page is the only side
        // that knows whether the tab exists in this shell at all.
        onRuntimeEvent('android:OpenRoute', (payload) => {
            if (boundedText(asRecord(payload).route, 32) === 'transfers') activeTab.set('transfers');
        }),
    ];
    return () => { for (const stop of stops) stop(); };
}
