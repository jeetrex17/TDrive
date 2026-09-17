import { derived, writable } from 'svelte/store';

export type TransferDirection = 'up' | 'down';
/**
 * A transfer's whole life, including the two states it spends most of its time
 * in on a phone: waiting behind other work, and stopped because the connection
 * went away. Without them the queue can only say "active", which is how a
 * stalled upload ends up looking identical to one that is moving.
 */
export type TransferStatus = 'queued' | 'active' | 'paused' | 'canceling' | 'done' | 'failed' | 'canceled';

/** The statuses that still have somewhere to go; the rest are finished. */
export const UNFINISHED_TRANSFER_STATUSES: readonly TransferStatus[] = ['queued', 'active', 'paused', 'canceling'];

export function isUnfinishedTransfer(status: TransferStatus): boolean {
    return UNFINISHED_TRANSFER_STATUSES.includes(status);
}

export interface TransferEvent {
    kind: 'transfer';
    id: string; // "xfer:<direction>:<callerId>"
    direction: TransferDirection;
    name: string;
    progress: number; // 0..100
    total: number; // bytes; 0 when unknown
    bytes: number; // transferred bytes derived from progress
    speed: number; // smoothed bytes/sec; 0 when unknown
    itemsDone?: number; // completed files for aggregate folder/import transfers
    itemsTotal?: number;
    status: TransferStatus;
    startedAt: number;
    finishedAt: number;
    /**
     * One sentence the transfer wants to keep after it has finished: on a phone,
     * where the file actually landed.
     *
     * A phone has no Finder to go and look in, so that answer is the whole point
     * of the download, and it used to be carried by a toast -- which is to say
     * it went past once and could not be asked for again.
     */
    note?: string;
}

export interface NoticeEvent {
    kind: 'event';
    id: string;
    level: string;
    title: string;
    body: string;
    ts: number;
    /**
     * How many times this same notice has arrived in a row. One means once, and
     * the row says nothing about it; more and it carries a count instead of
     * stacking identical rows down the list.
     */
    repeats?: number;
}

export type HistoryEvent = TransferEvent | NoticeEvent;

export type BellMode = 'idle' | 'active' | 'error';

/**
 * How many entries the history keeps; the oldest fall off the end.
 *
 * It lives with the store rather than with the module that enforces it because
 * it now also bounds what comes back off disk, and a restore that trusted a
 * stored count could hand the panel a list no cap had ever been applied to.
 */
export const HISTORY_CAP = 100;

// Newest first, capped by modules/notif-bell.ts. All mutations go through that
// module -- including the restore and the suspend that modules/transfer-
// persistence.ts asks it for -- so cap/dedupe/idempotency rules live in one
// place.
export const historyEvents = writable<HistoryEvent[]>([]);
export const notifPanelOpen = writable(false);
export const notifUnreadErrors = writable(0);

// Everything still on its way, whether or not bytes are moving this second.
// Queued and paused belong here, not in Recent: the work has not happened yet,
// and filing it under "recent" is how a queue forgets what it still owes.
export const activeTransfers = derived(historyEvents, (events) =>
    events.filter((e): e is TransferEvent => e.kind === 'transfer' && isUnfinishedTransfer(e.status)),
);

// Everything that is not still on its way: notices plus finished transfers,
// in arrival order (newest first).
export const recentEvents = derived(historyEvents, (events) =>
    events.filter((e) => !(e.kind === 'transfer' && isUnfinishedTransfer(e.status))),
);

export const bellMode = derived(
    [notifUnreadErrors, activeTransfers],
    ([unread, active]): BellMode => (unread > 0 ? 'error' : active.length > 0 ? 'active' : 'idle'),
);
