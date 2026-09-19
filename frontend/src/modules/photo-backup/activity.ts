// Projects the durable backup queue into the existing activity feed. One stable
// row is deliberately updated in place so a large camera roll cannot turn into
// an equally large notification log.
import type { PhotoBackupState } from '../../api/photo-backup';
import type { TransferItem } from '../../ui/notifications/notif-store';
import {
    markTransferDone, pushQueuedTransfer, pushTransferStart, removeTransfer, transferKey,
    updateTransferName, updateTransferProgress,
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

/** Bytes where the backend knows them, finished files where it does not. */
function queueProgress(bytesDone: number, bytesTotal: number, done: number, total: number): number {
    const fraction = bytesTotal > 0 ? bytesDone / bytesTotal : (total > 0 ? done / total : 0);
    return Math.max(0, Math.min(100, fraction * 100));
}

function totals(state: PhotoBackupState): { done: number; total: number } {
    const { complete, pending, uploading, failed, paused } = state.status;
    return { done: complete, total: complete + pending + uploading + failed + paused };
}

function resultName(state: PhotoBackupState): string {
    const { complete, failed, paused, phase } = state.status;
    if (!state.settings.enabled) return 'Photo backup stopped';
    if (failed > 0) return `Photo backup needs attention · ${failed} failed`;
    if (paused > 0 || state.manualPaused || phase === 'paused') return 'Photo backup paused';
    return `Photo backup completed · ${complete} ${complete === 1 ? 'item' : 'items'}`;
}

export function syncPhotoBackupActivity(state: PhotoBackupState): void {
    const { phase, bytesDone, bytesTotal } = state.status;
    // A paused backup ends its row rather than holding one open. Nothing is on
    // its way, so an active row with a progress bar was saying otherwise --
    // and Clear keeps what is still moving, which left "Photo backup paused"
    // sitting in the panel with no way to dismiss it.
    const terminal = phase === 'complete' || phase === 'failed' || phase === 'idle' || phase === 'paused';
    if (terminal) {
        if (visible) {
            // Each word stays close to its cause: switched off is over, paused
            // is put down until someone picks it up, and both are finished as
            // far as the panel and its Clear are concerned.
            const failed = phase === 'failed' || state.status.failed > 0;
            const off = !state.settings.enabled;
            const paused = state.status.paused > 0 || state.manualPaused || phase === 'paused';
            updateTransferName({ id: ACTIVITY_ID, direction: 'up', name: resultName(state) });
            markTransferDone({ id: ACTIVITY_ID, direction: 'up', status: failed ? 'failed' : off ? 'canceled' : paused ? 'stopped' : 'done' });
        }
        visible = false;
        return;
    }
    const queued = phase === 'queued' || phase === 'scanning';
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
    // The queue's progress, not the current file's: the bar is answering "how
    // far through the backup", and the file it happens to be on is one line
    // below.
    //
    // Bytes are the better measure -- a 4 GB video and a 2 MB photo are not the
    // same third of a three-item backup -- but the backend does not total the
    // queue's bytes today, so they arrive as zero and the honest fallback is
    // the files it has finished. A bar wired only to bytes would sit at 0 for
    // the whole run.
    updateTransferProgress({
        id: ACTIVITY_ID, direction: 'up',
        progress: queueProgress(bytesDone, bytesTotal, done, total),
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

/**
 * Whether a history id is the backup's own row. The rows that offer a stop
 * control ask here instead of matching the key themselves: the backup queue is
 * the backend's to schedule, and a rename of the id must not quietly leave them
 * offering a button that stops nothing.
 */
export function isPhotoBackupActivity(id: string): boolean {
    return id === transferKey('up', ACTIVITY_ID);
}
