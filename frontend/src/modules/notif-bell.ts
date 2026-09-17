// Notification bell — the single unified feedback surface in the top-right header.
// The idle bell stays quiet; hovering opens the full history panel, while click
// and keyboard activation preserve access on touch devices and for keyboard users.
// Active transfers and completed events remain merged chronologically.
//
// History lives in the ui/notifications stores (capped at 100, ephemeral).
// Modules elsewhere call `pushHistoryEvent(...)` and `pushTransferStart()` /
// `updateTransferProgress()` / `markTransferDone()` to feed the bell. This
// module owns every history mutation (cap, dedupe, idempotency); the Svelte
// components only render the stores and drive the open/close UI state.

import { get } from 'svelte/store';
import { state } from '../state';
import { cancelDownload, cancelUpload, cancelUploadById } from '../api';
import { clearDownloadSharePaths, forgetDownloadSharePath } from '../ui/mobile/mobile-shell-store';
import {
    historyEvents,
    isUnfinishedTransfer,
    notifPanelOpen,
    notifUnreadErrors,
    type NoticeEvent,
    type TransferDirection,
    type TransferEvent,
    type TransferStatus,
} from '../ui/notifications/notif-store';

const HISTORY_CAP = 100;


// Per-transfer speed sampling. Progress events arrive far more often than the
// rounded percent changes; samples land in this O(1) sidecar on every tick,
// and the visible entry (which notifies every store subscriber) only updates
// when the percent actually moves.
const speedSamples = new Map<string, { at: number; bytes: number; speed: number }>();

// Uploads the user stopped one at a time. The backend reports a cancelled file
// through the same error event as a real failure, so the row it belongs to
// would otherwise end as Failed and raise an error toast the user just asked
// for. Cleared when the id is reused by the next batch (see pushTransferStart).
const canceledUploads = new Set<number>();


// pushHistoryEvent enqueues a non-transfer event (folder created, drive
// joined, error, etc.). Returns the event id so callers can dedupe by
// reusing the same id, though that's rarely needed.
export function pushHistoryEvent({ level = 'info', title = '', body = '', ts }: { level?: string; title?: string; body?: string; ts?: number } = {}) {
    const id = `evt:${Date.now()}:${Math.random().toString(36).slice(2, 7)}`;
    const entry: NoticeEvent = {
        kind: 'event',
        id,
        level,
        title: String(title || ''),
        body: String(body || ''),
        ts: ts || Date.now(),
    };
    historyEvents.update((events) => {
        // The same notice arriving again is one thing happening twice, not two
        // things. Tapping upload while an upload runs says the same sentence
        // every time, and a list that repeats it verbatim buries whatever else
        // is in there. Only a run at the very top folds, so the history stays in
        // arrival order and an older identical notice keeps its own place.
        const newest = events[0];
        if (newest?.kind === 'event' && newest.level === entry.level
            && newest.title === entry.title && newest.body === entry.body) {
            const folded: NoticeEvent = {
                ...newest,
                ts: entry.ts,
                repeats: (newest.repeats ?? 1) + 1,
            };
            return [folded, ...events.slice(1)];
        }
        return [entry, ...events].slice(0, HISTORY_CAP);
    });
    if (level === 'error' && !get(notifPanelOpen)) {
        notifUnreadErrors.update((n) => n + 1);
    }
    return id;
}

// pushTransferStart begins tracking an upload or download. id should be
// unique per transfer (msg_id, upload id, etc.). id=0 is valid — upload
// IDs are zero-based, so don't reject falsy.
export function pushTransferStart({ id, direction, name, total = 0 }: { id: string | number; direction: TransferDirection; name?: string; total?: number }) {
    if (id == null || !direction) return;
    const key = transferKey(direction, id);
    if (direction === 'down') forgetDownloadSharePath(key);
    speedSamples.delete(key);
    // Upload IDs restart at 0 with every batch, so a mark left by the last
    // batch must not make this file's first failure read as a cancellation.
    if (direction === 'up') canceledUploads.delete(Number(id));
    const entry: TransferEvent = {
        kind: 'transfer',
        id: key,
        direction,
        name: String(name || ''),
        progress: 0,
        total: Number(total) || 0,
        bytes: 0,
        speed: 0,
        status: 'active',
        startedAt: Date.now(),
        finishedAt: 0,
    };
    // De-dup: an entry with this key is replaced, not duplicated.
    historyEvents.update((events) => [entry, ...events.filter((e) => e.id !== key)].slice(0, HISTORY_CAP));
    return key;
}

/**
 * Makes waiting work visible before the scheduler is ready to dispatch it.
 * Callers must promote the same id with pushTransferStart when its first byte
 * is requested, which replaces this row in place rather than duplicating it.
 */
export function pushQueuedTransfer({ id, direction, name, total = 0 }: { id: string | number; direction: TransferDirection; name?: string; total?: number }) {
    if (id == null || !direction) return;
    const key = transferKey(direction, id);
    const entry: TransferEvent = {
        kind: 'transfer',
        id: key,
        direction,
        name: String(name || ''),
        progress: 0,
        total: Math.max(0, Number(total) || 0),
        bytes: 0,
        speed: 0,
        status: 'queued',
        startedAt: Date.now(),
        finishedAt: 0,
    };
    historyEvents.update((events) => [entry, ...events.filter((event) => event.id !== key)].slice(0, HISTORY_CAP));
    return key;
}

export function updateTransferProgress({
    id,
    direction,
    progress,
    bytes: exactBytes,
    total: exactTotal,
    itemsDone,
    itemsTotal,
}: {
    id: string | number;
    direction: TransferDirection;
    progress: number;
    bytes?: number;
    total?: number;
    itemsDone?: number;
    itemsTotal?: number;
}) {
    const key = transferKey(direction, id);
    const entry = findUnfinishedTransfer(key);
    if (!entry) return;
    const value = Math.max(entry.progress, Math.max(0, Math.min(100, Number(progress) || 0)));
    const suppliedTotal = Number(exactTotal);
    const total = Number.isFinite(suppliedTotal) && suppliedTotal >= 0
        ? Math.max(entry.total, suppliedTotal)
        : entry.total;

    // Track transferred bytes and a smoothed speed for the row meta.
    let bytes = entry.bytes;
    let speed = entry.speed;
    const suppliedBytes = Number(exactBytes);
    if (Number.isFinite(suppliedBytes) && suppliedBytes >= 0) {
        bytes = Math.max(entry.bytes, total > 0 ? Math.min(total, suppliedBytes) : suppliedBytes);
    } else if (total > 0) {
        bytes = Math.max(entry.bytes, (value / 100) * total);
    }
    if (total > 0) {
        const now = Date.now();
        const prev = speedSamples.get(key);
        if (prev && now > prev.at) {
            const inst = (bytes - prev.bytes) / ((now - prev.at) / 1000);
            if (Number.isFinite(inst) && inst >= 0) {
                speed = prev.speed ? prev.speed * 0.6 + inst * 0.4 : inst;
            }
        }
        speedSamples.set(key, { at: now, bytes, speed });
    }

    const nextItemsDone = Number.isFinite(Number(itemsDone))
        ? Math.max(entry.itemsDone ?? 0, Math.max(0, Number(itemsDone)))
        : entry.itemsDone;
    const nextItemsTotal = Number.isFinite(Number(itemsTotal))
        ? Math.max(entry.itemsTotal ?? 0, Math.max(0, Number(itemsTotal)))
        : entry.itemsTotal;
    const unchanged = Math.round(entry.progress) === Math.round(value)
        && entry.total === total
        && entry.bytes === bytes
        && entry.itemsDone === nextItemsDone
        && entry.itemsTotal === nextItemsTotal;
    if (unchanged) return; // skip render noise
    historyEvents.update((events) =>
        events.map((e) => (e.id === key && e.kind === 'transfer'
            ? { ...e, progress: value, bytes, total, speed, itemsDone: nextItemsDone, itemsTotal: nextItemsTotal }
            : e)),
    );
}

// updateTransferName changes an active transfer's title in place (no progress
// reset), used to show an import's live phase: extracting, adding folders, etc.
export function updateTransferName({ id, direction, name }: { id: string | number; direction: TransferDirection; name: string }) {
    const key = transferKey(direction, id);
    const entry = findUnfinishedTransfer(key);
    if (!entry) return;
    const next = String(name || '');
    if (entry.name === next) return;
    historyEvents.update((events) =>
        events.map((e) => (e.id === key && e.kind === 'transfer' ? { ...e, name: next } : e)),
    );
}

export function markTransferDone({ id, direction, status = 'done' }: { id: string | number; direction: TransferDirection; status?: TransferStatus }) {
    const key = transferKey(direction, id);
    // Idempotent: don't downgrade or rewrite an already-terminal entry
    // (e.g. a safety sweep firing 'done' on an entry that already failed).
    const entry = findUnfinishedTransfer(key);
    if (!entry) return;
    speedSamples.delete(key);
    historyEvents.update((events) =>
        events.map((e) =>
            e.id === key && e.kind === 'transfer'
                ? { ...e, status, progress: status === 'done' ? 100 : e.progress, finishedAt: Date.now() }
                : e,
        ),
    );
    if (status === 'failed' && !get(notifPanelOpen)) {
        notifUnreadErrors.update((n) => n + 1);
    }
}

export function clearHistory() {
    // Keep everything still on its way -- running, waiting, or stopped short of
    // finishing. Clear is for the log of what already happened, and work the
    // app still owes the user is not a log entry. Drop everything else.
    historyEvents.update((events) =>
        events.filter((e) => e.kind === 'transfer' && isUnfinishedTransfer(e.status)),
    );
    clearDownloadSharePaths();
    notifUnreadErrors.set(0);
}

// cancelTransfersInDirection cancels every upload/import, or the one active
// backend download. Queued downloads remain queued because the backend has no
// per-queue cancellation API.
// Rows are not marked here: the backend reports the real per-file outcome, so
// a file that already finished (and committed) ends as Done while aborted
// ones end as Canceled (see the upload_error / download handlers).
export function cancelTransfersInDirection(direction: TransferDirection): void {
    if (direction === 'down') {
        const activeDownloadId = state.activeDownloadId;
        if (activeDownloadId === null) return;
        const matchesActiveDownload = (entry: TransferEvent) => entry.id === transferKey('down', activeDownloadId);
        markTransfersCanceling(direction, matchesActiveDownload);
        state.cancelingDownload = true;
        void cancelDownload().catch(() => {
            state.cancelingDownload = false;
            restoreCanceledTransfers(direction, matchesActiveDownload);
            pushHistoryEvent({ level: 'error', title: 'Could not cancel download', body: 'Your download is still running.' });
        });
        return;
    }

    markTransfersCanceling(direction);
    state.cancelingUpload = true;
    void cancelUpload().catch(() => {
        state.cancelingUpload = false;
        restoreCanceledTransfers(direction);
        pushHistoryEvent({ level: 'error', title: 'Could not cancel uploads', body: 'Your uploads are still running.' });
    });
}

// cancelSingleUpload stops one file of an upload batch. Several uploads run at
// once, so the direction-wide cancel would take the others down with it. Like
// that one, it leaves the row alone: the backend reports the real outcome, so a
// file that beat the cancel to Telegram still ends as Done.
export function cancelSingleUpload(uploadId: number): void {
    if (!Number.isFinite(uploadId)) return;
    canceledUploads.add(uploadId);
    markTransfersCanceling('up', (entry) => entry.id === transferKey('up', uploadId));
    void cancelUploadById(uploadId).catch(() => {
        canceledUploads.delete(uploadId);
        restoreCanceledTransfers('up', (entry) => entry.id === transferKey('up', uploadId));
        pushHistoryEvent({ level: 'error', title: 'Could not cancel upload', body: 'The upload is still running.' });
    });
}

export function wasUploadCanceled(uploadId: number): boolean {
    return canceledUploads.has(uploadId);
}

function transferKey(direction: TransferDirection, id: string | number): string {
    return `xfer:${direction}:${id}`;
}

// The entry a progress, rename or finish update is allowed to change: one that
// has not reached a terminal state. Matching on 'active' alone would make every
// one of those a silent no-op the moment a transfer can also be queued.
function findUnfinishedTransfer(key: string): TransferEvent | null {
    const entry = get(historyEvents).find((e) => e.id === key);
    if (!entry || entry.kind !== 'transfer' || !isUnfinishedTransfer(entry.status)) return null;
    return entry;
}

function markTransfersCanceling(direction: TransferDirection, matches: (entry: TransferEvent) => boolean = () => true): void {
    historyEvents.update((events) => events.map((event) => (
        event.kind === 'transfer'
        && event.direction === direction
        && isUnfinishedTransfer(event.status)
        && matches(event)
            ? { ...event, status: 'canceling' }
            : event
    )));
}

function restoreCanceledTransfers(direction: TransferDirection, matches: (entry: TransferEvent) => boolean = () => true): void {
    historyEvents.update((events) => events.map((event) => (
        event.kind === 'transfer'
        && event.direction === direction
        && event.status === 'canceling'
        && matches(event)
            ? { ...event, status: 'active' }
            : event
    )));
}
