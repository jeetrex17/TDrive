// Projects the durable backup queue into the existing activity feed. One stable
// row is deliberately updated in place so a large camera roll cannot turn into
// an equally large notification log.
import type { PhotoBackupState } from '../../api/photo-backup';
import type { TransferItem } from '../../ui/notifications/notif-store';
import {
    markTransferDone, pushQueuedTransfer, pushTransferStart, removeTransfer, updateTransferName, updateTransferProgress,
} from '../notif-bell';

const ACTIVITY_ID = 'photo-backup';
let visible = false;
/** Whether the visible row is the waiting kind, so a change of kind re-pushes it. */
let waiting = false;

/**
 * The row's title says what the run is; the file being sent is listed under it
 * like the files of any other batch. Putting the filename in the title instead
 * left the title changing every few seconds and the queue's own progress with
 * nowhere to be said.
 */
function currentName(state: PhotoBackupState): string {
    if (state.status.phase === 'paused') return 'Photo backup paused';
    if (state.status.phase === 'scanning') return 'Scanning photos and videos';
    if (state.status.currentFile) return 'Photo backup';
    return 'Preparing photo backup';
}

/** The one file the backup has in flight, or nothing while it is between files. */
function currentItem(state: PhotoBackupState): readonly TransferItem[] {
    const parts = state.status.currentFile.split(/[\\/]/).filter(Boolean);
    const file = parts[parts.length - 1];
    if (!file) return [];
    return [{
        // The path, not the display name: two photos in different albums can
        // share a filename, and the key is what keeps a bar with its file.
        key: state.status.currentFile,
        name: file,
        progress: state.status.currentFilePercent,
        total: state.status.currentFileBytesTotal,
    }];
}

function totals(state: PhotoBackupState): { done: number; total: number } {
    const { complete, pending, uploading, failed, paused } = state.status;
    return { done: complete, total: complete + pending + uploading + failed + paused };
}

function resultName(state: PhotoBackupState): string {
    const { complete, failed, paused } = state.status;
    if (!state.settings.enabled) return 'Photo backup stopped';
    if (failed > 0) return `Photo backup needs attention · ${failed} failed`;
    if (paused > 0 || state.manualPaused) return 'Photo backup paused';
    return `Photo backup completed · ${complete} ${complete === 1 ? 'item' : 'items'}`;
}

export function syncPhotoBackupActivity(state: PhotoBackupState): void {
    const { phase, bytesDone, bytesTotal } = state.status;
    const terminal = phase === 'complete' || phase === 'failed' || phase === 'idle';
    if (terminal) {
        if (visible) {
            const failed = phase === 'failed' || state.status.failed > 0;
            const paused = !state.settings.enabled || state.status.paused > 0 || state.manualPaused;
            updateTransferName({ id: ACTIVITY_ID, direction: 'up', name: resultName(state) });
            markTransferDone({ id: ACTIVITY_ID, direction: 'up', status: failed ? 'failed' : paused ? 'canceled' : 'done' });
        }
        visible = false;
        return;
    }
    const queued = phase === 'paused' || phase === 'queued' || phase === 'scanning';
    // Pushing the row again would replace it, and with it the clock it started
    // and the rate sampled off it -- which is the working behind "4 min left".
    // So it is pushed once, and again only when it changes between waiting and
    // running, which is the one thing an update cannot say for it.
    if (!visible || queued !== waiting) {
        (queued ? pushQueuedTransfer : pushTransferStart)({
            id: ACTIVITY_ID, direction: 'up', name: currentName(state), total: bytesTotal,
        });
    } else {
        updateTransferName({ id: ACTIVITY_ID, direction: 'up', name: currentName(state) });
    }
    waiting = queued;
    const { done, total } = totals(state);
    // The queue's bytes, not the current file's: the bar is answering "how far
    // through the backup", and the file it happens to be on is one line below.
    updateTransferProgress({
        id: ACTIVITY_ID, direction: 'up',
        progress: bytesTotal > 0 ? (bytesDone / bytesTotal) * 100 : 0,
        bytes: bytesDone, total: bytesTotal, itemsDone: done, itemsTotal: total,
        items: currentItem(state), itemsActive: state.status.uploading,
    });
    visible = true;
}

export function clearPhotoBackupActivity(): void {
    removeTransfer({ id: ACTIVITY_ID, direction: 'up' });
    visible = false;
    waiting = false;
}

export function isPhotoBackupActivity(id: string): boolean {
    return id === `xfer:up:${ACTIVITY_ID}`;
}
