// Notification bell — the single unified feedback surface in the top-right
// header. Three layers of detail:
//
//   • Idle bell — quiet, dim. Nothing happening.
//   • Hover popover — only when transfers are active. Mini progress list.
//   • Click panel — full history. Active transfers + Recent (notifications
//     and completed transfers, merged chronologically).
//
// History lives in the ui/notifications stores (capped at 100, ephemeral).
// Modules elsewhere call `pushHistoryEvent(...)` and `pushTransferStart()` /
// `updateTransferProgress()` / `markTransferDone()` to feed the bell. This
// module owns every history mutation (cap, dedupe, idempotency); the Svelte
// components only render the stores and drive the open/close UI state.

import { get } from 'svelte/store';
import { state } from '../state';
import NotifBell from '../ui/notifications/NotifBell.svelte';
import {
    historyEvents,
    notifPanelOpen,
    notifUnreadErrors,
    type NoticeEvent,
    type TransferDirection,
    type TransferEvent,
    type TransferStatus,
} from '../ui/notifications/notif-store';
import { mountSvelte, type SvelteMountHandle } from '../ui/mount';

const HISTORY_CAP = 100;

let bellHandle: SvelteMountHandle<Record<string, unknown>> | null = null;

// Per-transfer speed sampling. Progress events arrive far more often than the
// rounded percent changes; samples land in this O(1) sidecar on every tick,
// and the visible entry (which notifies every store subscriber) only updates
// when the percent actually moves.
const speedSamples = new Map<string, { at: number; bytes: number; speed: number }>();

export function setupNotifBell() {
    const host = document.getElementById('notif-bell-root');
    if (!host || bellHandle) return;

    host.replaceChildren();
    bellHandle = mountSvelte(NotifBell, {
        target: host,
        props: {
            onCancelDirection: cancelTransfersInDirection,
            onClearHistory: clearHistory,
        },
    });
}

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
    historyEvents.update((events) => [entry, ...events].slice(0, HISTORY_CAP));
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
    speedSamples.delete(key);
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
    const entry = findActiveTransfer(key);
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
    const entry = findActiveTransfer(key);
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
    const entry = findActiveTransfer(key);
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
    // Keep active transfers. Drop everything else.
    historyEvents.update((events) =>
        events.filter((e) => e.kind === 'transfer' && e.status === 'active'),
    );
    notifUnreadErrors.set(0);
}

// cancelTransfersInDirection cancels the active upload/import or download.
// Rows are not marked here: the backend reports the real per-file outcome, so
// a file that already finished (and committed) ends as Done while aborted
// ones end as Canceled (see the upload_error / download handlers).
function cancelTransfersInDirection(direction: TransferDirection) {
    const app = (window as any)?.go?.main?.App;
    try {
        if (direction === 'down') {
            app?.CancelDownload?.();
            state.cancelingDownload = true;
        } else {
            app?.CancelUpload?.();
            state.cancelingUpload = true;
        }
    } catch { /* binding optional */ }
}

function transferKey(direction: TransferDirection, id: string | number): string {
    return `xfer:${direction}:${id}`;
}

function findActiveTransfer(key: string): TransferEvent | null {
    const entry = get(historyEvents).find((e) => e.id === key);
    if (!entry || entry.kind !== 'transfer' || entry.status !== 'active') return null;
    return entry;
}
