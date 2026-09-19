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
import { boundedText } from '../api/shared';
import { formatBytes } from '../utils';
import {
    HISTORY_CAP,
    historyEvents,
    isUnfinishedTransfer,
    MAX_TRANSFER_ITEMS,
    notifPanelOpen,
    notifUnreadErrors,
    type HistoryEvent,
    type NoticeEvent,
    type TransferDirection,
    type TransferEvent,
    type TransferItem,
    type TransferStatus,
} from '../ui/notifications/notif-store';

/**
 * Longest filename a row keeps. Names are user data on their way to the DOM,
 * and a row that clips at its own width has nothing to gain from more.
 */
const MAX_TRANSFER_ITEM_NAME = 120;


// Per-transfer speed sampling. Progress events arrive far more often than the
// row redraws; samples land in this O(1) sidecar on every tick, and the visible
// entry (which notifies every store subscriber and the saver behind them) only
// updates when a figure the row actually draws has moved.
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
    items,
    itemsActive,
}: {
    id: string | number;
    direction: TransferDirection;
    progress: number;
    bytes?: number;
    total?: number;
    itemsDone?: number;
    itemsTotal?: number;
    items?: readonly TransferItem[];
    itemsActive?: number;
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
    // Sampled off the byte count, which an aggregate has even when it has no
    // total to compare it against: "how fast" and "how far" are separate
    // questions, and a batch can answer the first long before the second.
    if (total > 0 || bytes > 0) {
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
    // Bytes moving again is the only trustworthy sign that a transfer paused by
    // a backgrounded app survived being suspended, so the tick carrying them is
    // what lifts the row back out of paused. Flipping every paused row the
    // moment the app returns would instead promise that a connection dropped an
    // hour ago is still good.
    const status: TransferStatus = entry.status === 'paused' ? 'active' : entry.status;
    const nextItems = items === undefined ? entry.items : boundTransferItems(items);
    const nextItemsActive = Number.isFinite(Number(itemsActive))
        ? Math.max(0, Math.floor(Number(itemsActive)))
        : entry.itemsActive;
    // Everything above is compared at the precision the row renders it -- whole
    // percent, and the rounded figure formatBytes prints -- because a transfer
    // reports bytes tens of times per drawn step and an exact comparison here
    // made every one of those a store write, waking each derived store and the
    // saver subscribed behind them. The sampling above already ran, so the rate
    // and the estimate built on it still see every tick.
    const sameFigures = status === entry.status
        && Math.round(entry.progress) === Math.round(value)
        && entry.total === total
        && formatBytes(entry.bytes) === formatBytes(bytes)
        && entry.itemsDone === nextItemsDone
        && entry.itemsTotal === nextItemsTotal
        && entry.itemsActive === nextItemsActive
        && sameTransferItems(entry.items, nextItems);
    // The tick that completes the transfer lands in full whatever it would
    // redraw. These figures outlive the row -- markTransferDone freezes them and
    // the next launch restores them -- so the last one written must be the true
    // one, not the last one that happened to be worth drawing. Both halves also
    // require an actual change, so a backend that keeps ticking at 100% writes
    // once and then goes quiet again.
    const completing = (value >= 100 && value !== entry.progress)
        || (total > 0 && bytes >= total && bytes !== entry.bytes);
    if (sameFigures && !completing) return; // skip render noise
    historyEvents.update((events) =>
        events.map((e) => (e.id === key && e.kind === 'transfer'
            ? {
                ...e, status, progress: value, bytes, total, speed,
                itemsDone: nextItemsDone, itemsTotal: nextItemsTotal,
                items: nextItems, itemsActive: nextItemsActive,
            }
            : e)),
    );
}

/**
 * The list a row is allowed to draw: at most MAX_TRANSFER_ITEMS files, each
 * with a name short enough to render and figures that cannot be read as a
 * transfer moving backwards. Every producer goes through here, so no caller can
 * hand the DOM an unbounded name or the store an unbounded list.
 */
function boundTransferItems(items: readonly TransferItem[]): readonly TransferItem[] {
    return items.slice(0, MAX_TRANSFER_ITEMS).map((item) => ({
        key: boundedText(item.key, 64),
        name: boundedText(item.name, MAX_TRANSFER_ITEM_NAME),
        progress: Math.max(0, Math.min(100, Number(item.progress) || 0)),
        total: Math.max(0, Number(item.total) || 0),
    }));
}

/**
 * Whether a redraw would show the same thing. Compared at the precision the row
 * renders -- whole percent -- because progress events arrive many times per
 * point and each store write wakes every subscriber.
 */
function sameTransferItems(a: readonly TransferItem[] | undefined, b: readonly TransferItem[] | undefined): boolean {
    if (a === b) return true;
    if (!a || !b || a.length !== b.length) return false;
    return a.every((item, index) => item.key === b[index].key
        && item.name === b[index].name
        && item.total === b[index].total
        && Math.round(item.progress) === Math.round(b[index].progress));
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

/**
 * Attaches the sentence a finished transfer should keep -- where a phone
 * download landed. Set after the transfer is terminal, so unlike the other
 * updaters here it looks the entry up wherever it is rather than only among the
 * unfinished.
 */
export function setTransferNote({ id, direction, note }: { id: string | number; direction: TransferDirection; note: string }) {
    const key = transferKey(direction, id);
    const text = String(note || '');
    if (!text) return;
    historyEvents.update((events) =>
        events.map((e) => (e.id === key && e.kind === 'transfer' ? { ...e, note: text } : e)),
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
                // Nothing is in flight any more, so the per-file list goes with
                // the bar: a finished row that still named three files would be
                // reporting work that has already stopped.
                ? {
                    ...e, status, finishedAt: Date.now(), items: undefined, itemsActive: 0,
                    progress: status === 'done' ? 100 : e.progress,
                }
                : e,
        ),
    );
    if (status === 'failed' && !get(notifPanelOpen)) {
        notifUnreadErrors.update((n) => n + 1);
    }
}

/** Removes scoped work outright when its account or drive is no longer active. */
export function removeTransfer({ id, direction }: { id: string | number; direction: TransferDirection }): void {
    const key = transferKey(direction, id);
    speedSamples.delete(key);
    historyEvents.update((events) => events.filter((event) => event.id !== key));
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
        const canceling = markTransfersCanceling(direction, matchesActiveDownload);
        state.cancelingDownload = true;
        void cancelDownload().catch(() => {
            state.cancelingDownload = false;
            restoreCanceledTransfers(canceling);
            pushHistoryEvent({ level: 'error', title: 'Could not cancel download', body: 'Your download is still running.' });
        });
        return;
    }

    const canceling = markTransfersCanceling(direction);
    state.cancelingUpload = true;
    void cancelUpload().catch(() => {
        state.cancelingUpload = false;
        restoreCanceledTransfers(canceling);
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
    const canceling = markTransfersCanceling('up', (entry) => entry.id === transferKey('up', uploadId));
    void cancelUploadById(uploadId).catch(() => {
        canceledUploads.delete(uploadId);
        restoreCanceledTransfers(canceling);
        pushHistoryEvent({ level: 'error', title: 'Could not cancel upload', body: 'The upload is still running.' });
    });
}

/**
 * Stops one file listed under an aggregate upload row.
 *
 * The key is the file's upload id wherever the backend can stop it on its own,
 * which is every batch and import file. Photo backup keys its file by path
 * instead -- its queue is the backend's to schedule, not ours to interrupt --
 * and a non-numeric key is how that says so.
 */
export function cancelUploadFile(key: string): void {
    const uploadId = Number(key);
    if (!Number.isInteger(uploadId)) return;
    cancelSingleUpload(uploadId);
}

export function wasUploadCanceled(uploadId: number): boolean {
    return canceledUploads.has(uploadId);
}

/**
 * Files the history a previous run of the app left behind underneath whatever
 * this one has raised so far.
 *
 * Underneath, and never over the top. The bell is not reliably empty when the
 * restore lands -- a connectivity notice or a download queued from a deep link
 * can beat the dashboard onto the screen -- and those are both newer and, in
 * the download's case, actually running. A restored row carrying an id one of
 * them already has is the dead copy of it and is dropped.
 */
export function loadHistorySnapshot(events: readonly HistoryEvent[]): void {
    if (events.length === 0) return;
    historyEvents.update((current) => {
        const live = new Set(current.map((event) => event.id));
        return [...current, ...events.filter((event) => !live.has(event.id))].slice(0, HISTORY_CAP);
    });
}

/**
 * Stops the clock on everything that is running, for the one case where that is
 * true of all of it at once: the OS has suspended the whole process behind a
 * backgrounded app, so not a byte moves until it comes back.
 *
 * What this prevents is a screen full of rows drawing live bars over a frozen
 * process -- and, if the OS then reclaims the app rather than resuming it, a
 * saved record that claims the same thing. Only running transfers move: queued
 * work was not going anywhere anyway, and a cancel that was already in flight
 * keeps its own state.
 */
export function pauseRunningTransfers(): void {
    const running = get(historyEvents).some((event) => event.kind === 'transfer' && event.status === 'active');
    if (!running) return;
    historyEvents.update((events) => events.map((event) => (
        event.kind === 'transfer' && event.status === 'active'
            ? { ...event, status: 'paused' }
            : event
    )));
}

/**
 * The one place the `xfer:<direction>:<callerId>` grammar is written. Exported
 * because the surfaces that ask what a row is -- whether the backend can stop
 * it on its own, whether it is the photo backup -- were re-deriving it with
 * string literals, which a rename would have left silently wrong.
 */
export function transferKey(direction: TransferDirection, id: string | number): string {
    return `xfer:${direction}:${id}`;
}

/** The same grammar read back, or null for a string that is not one of our keys. */
export function parseTransferKey(key: string): { direction: TransferDirection; id: string } | null {
    const prefix = 'xfer:';
    if (!key.startsWith(prefix)) return null;
    const rest = key.slice(prefix.length);
    // The caller id may itself contain colons ('file:42'), so only the
    // direction is split off; the remainder is the id whole.
    const sep = rest.indexOf(':');
    if (sep <= 0 || sep === rest.length - 1) return null;
    const direction = rest.slice(0, sep);
    if (direction !== 'up' && direction !== 'down') return null;
    return { direction, id: rest.slice(sep + 1) };
}

// The entry a progress, rename or finish update is allowed to change: one that
// has not reached a terminal state. Matching on 'active' alone would make every
// one of those a silent no-op the moment a transfer can also be queued.
function findUnfinishedTransfer(key: string): TransferEvent | null {
    const entry = get(historyEvents).find((e) => e.id === key);
    if (!entry || entry.kind !== 'transfer' || !isUnfinishedTransfer(entry.status)) return null;
    return entry;
}

/**
 * Marks the rows a cancel is about, and hands back what each of them was.
 *
 * The status a row leaves behind is the only way back if the cancel fails, and
 * it is per row: a batch on a backgrounded phone is a mix of running, waiting
 * and paused work. Returning it keeps that answer with the one call that can
 * still see it, rather than in module state a successful cancel would leak.
 * Rows already canceling belong to a cancel of their own and are left out.
 */
function markTransfersCanceling(direction: TransferDirection, matches: (entry: TransferEvent) => boolean = () => true): Map<string, TransferStatus> {
    const previous = new Map<string, TransferStatus>();
    historyEvents.update((events) => events.map((event) => {
        if (event.kind !== 'transfer'
            || event.direction !== direction
            || event.status === 'canceling'
            || !isUnfinishedTransfer(event.status)
            || !matches(event)) return event;
        previous.set(event.id, event.status);
        return { ...event, status: 'canceling' };
    }));
    return previous;
}

/**
 * Puts back exactly what the failed cancel took.
 *
 * Every row returns to its own status, never to 'active'. A paused row promoted
 * to active draws a live bar over a process moving no bytes -- the very thing
 * pauseRunningTransfers exists to prevent, and the shape of the iOS bug: the app
 * is suspended, the user comes back, taps Cancel all, and the RPC is rejected.
 * A queued row promoted the same way claims a turn it has not been given.
 */
function restoreCanceledTransfers(previous: ReadonlyMap<string, TransferStatus>): void {
    if (previous.size === 0) return;
    historyEvents.update((events) => events.map((event) => {
        if (event.kind !== 'transfer' || event.status !== 'canceling') return event;
        const prior = previous.get(event.id);
        return prior ? { ...event, status: prior } : event;
    }));
}
