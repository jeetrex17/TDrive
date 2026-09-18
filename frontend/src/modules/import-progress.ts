import { asRecord, boundedText, nonNegativeNumber } from '../api/shared';
import { MAX_TRANSFER_ITEMS, type TransferItem } from '../ui/notifications/notif-store';

export interface ImportProgress {
    total: number;
    done: number;
    failed: number;
    progress: number;
    /** Bytes the import has actually sent, partial files included. */
    bytes: number;
    /** The files uploading right now; activeCount says how many there are in all. */
    active: readonly TransferItem[];
    activeCount: number;
}

export interface ImportProgressSnapshot {
    total?: unknown;
    done?: unknown;
    failed?: unknown;
    progress?: unknown;
    bytes?: unknown;
    active?: unknown;
    activeCount?: unknown;
}

export function createImportProgress(): ImportProgress {
    return { total: 0, done: 0, failed: 0, progress: 0, bytes: 0, active: [], activeCount: 0 };
}

function monotonicCount(value: unknown, previous: number): number {
    const parsed = Number(value);
    if (!Number.isFinite(parsed)) return previous;
    return Math.max(previous, Math.floor(Math.max(0, parsed)));
}

function monotonicPercent(value: unknown, previous: number): number {
    const parsed = Number(value);
    if (!Number.isFinite(parsed)) return previous;
    return Math.max(previous, Math.min(100, Math.max(0, parsed)));
}

/**
 * The files the backend says are in flight.
 *
 * Unlike the counters beside it this is a live set rather than a running total,
 * so it is replaced outright: a file that finished between two events should
 * leave the list rather than linger in it. The cap is applied again here
 * because the payload is the one thing in this module that comes from outside.
 */
function activeItems(value: unknown, previous: readonly TransferItem[]): readonly TransferItem[] {
    if (!Array.isArray(value)) return previous;
    return value.slice(0, MAX_TRANSFER_ITEMS).map((raw): TransferItem => {
        const file = asRecord(raw);
        return {
            key: String(nonNegativeNumber(file.id)),
            name: boundedText(file.name, 120),
            progress: Math.min(100, nonNegativeNumber(file.percent)),
            total: nonNegativeNumber(file.size),
        };
    });
}

// Import progress arrives as backend aggregates, so every update is O(1) and
// the retained state stays constant regardless of the number of files: the
// counters are single numbers, and the file list holds only the handful of
// uploads that can be in flight at once.
export function reduceImportProgress(
    previous: ImportProgress,
    snapshot: ImportProgressSnapshot,
): ImportProgress {
    const total = monotonicCount(snapshot.total, previous.total);
    const done = monotonicCount(snapshot.done, previous.done);
    const failed = monotonicCount(snapshot.failed, previous.failed);
    const bytes = monotonicCount(snapshot.bytes, previous.bytes);
    let progress = monotonicPercent(snapshot.progress, previous.progress);
    const active = activeItems(snapshot.active, previous.active);
    const activeCount = Math.max(active.length, Math.floor(nonNegativeNumber(snapshot.activeCount)));

    if (total > 0 && done + failed >= total) progress = 100;
    return { total, done, failed, progress, bytes, active, activeCount };
}
