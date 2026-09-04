import { derived, writable } from 'svelte/store';

export type TransferDirection = 'up' | 'down';
export type TransferStatus = 'active' | 'done' | 'failed' | 'canceled';

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
}

export interface NoticeEvent {
    kind: 'event';
    id: string;
    level: string;
    title: string;
    body: string;
    ts: number;
}

export type HistoryEvent = TransferEvent | NoticeEvent;

export type BellMode = 'idle' | 'active' | 'error';

// Newest first, capped by modules/notif-bell.ts. All mutations go through
// that module so cap/dedupe/idempotency rules live in one place.
export const historyEvents = writable<HistoryEvent[]>([]);
export const notifPanelOpen = writable(false);
export const notifHoverOpen = writable(false);
export const notifUnreadErrors = writable(0);

export const activeTransfers = derived(historyEvents, (events) =>
    events.filter((e): e is TransferEvent => e.kind === 'transfer' && e.status === 'active'),
);

// Everything that is not an in-flight transfer: notices plus finished
// transfers, in arrival order (newest first).
export const recentEvents = derived(historyEvents, (events) =>
    events.filter((e) => !(e.kind === 'transfer' && e.status === 'active')),
);

export const bellMode = derived(
    [notifUnreadErrors, activeTransfers],
    ([unread, active]): BellMode => (unread > 0 ? 'error' : active.length > 0 ? 'active' : 'idle'),
);
