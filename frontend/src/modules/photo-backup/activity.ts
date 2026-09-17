// Projects the durable backup queue into the existing activity feed. One stable
// row is deliberately updated in place so a large camera roll cannot turn into
// an equally large notification log.
import type { PhotoBackupState } from '../../api/photo-backup';
import {
    markTransferDone, pushQueuedTransfer, pushTransferStart, removeTransfer, updateTransferName, updateTransferProgress,
} from '../notif-bell';

const ACTIVITY_ID = 'photo-backup';
let visible = false;

function currentName(state: PhotoBackupState): string {
    const parts = state.status.currentFile.split(/[\\/]/).filter(Boolean);
    const file = parts[parts.length - 1];
    if (file) return `Backing up ${file}`;
    if (state.status.phase === 'paused') return 'Photo backup paused';
    if (state.status.phase === 'scanning') return 'Scanning photos and videos';
    return 'Preparing photo backup';
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
    const { phase, currentFileBytesDone, currentFileBytesTotal, currentFilePercent } = state.status;
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
    const start = queued ? pushQueuedTransfer : pushTransferStart;
    start({ id: ACTIVITY_ID, direction: 'up', name: currentName(state), total: currentFileBytesTotal });
    const { done, total } = totals(state);
    updateTransferProgress({
        id: ACTIVITY_ID, direction: 'up', progress: currentFilePercent,
        bytes: currentFileBytesDone, total: currentFileBytesTotal, itemsDone: done, itemsTotal: total,
    });
    visible = true;
}

export function clearPhotoBackupActivity(): void {
    removeTransfer({ id: ACTIVITY_ID, direction: 'up' });
    visible = false;
}

export function isPhotoBackupActivity(id: string): boolean {
    return id === `xfer:up:${ACTIVITY_ID}`;
}
