/**
 * The transfer log, kept across restarts.
 *
 * Everything the bell knows -- the downloads still waiting their turn, the
 * uploads that failed, where a finished download landed -- lived only in
 * memory, which on a phone means it lived until the OS wanted the RAM back.
 * Backgrounded apps are killed routinely and without warning, and a queue that
 * forgets what it owed the moment that happens is a queue nobody can hand a
 * long upload to and walk away from.
 *
 * Two rules shape everything below.
 *
 * Nothing measured per tick is written down. Progress, bytes and speed move
 * many times a second and none of them survive their own process: whatever the
 * bar said, a restored transfer has moved zero bytes, because the thing that
 * was moving them is gone. Leaving them out is also what keeps the record
 * byte-identical from one tick to the next, so a ten-minute download compares
 * equal every time and is written once rather than six hundred times.
 *
 * And nothing is serialised where the events arrive. The store subscription
 * only sets a flag; a timer does the work, at most once every few seconds --
 * plus once more the instant the app is hidden, which on iOS is the last
 * moment the process is guaranteed to be scheduled at all.
 */

import { get } from 'svelte/store';
import { isIOSPlatform } from '../api';
import {
    HISTORY_CAP,
    historyEvents,
    isUnfinishedTransfer,
    type HistoryEvent,
    type NoticeEvent,
    type TransferDirection,
    type TransferEvent,
    type TransferStatus,
} from '../ui/notifications/notif-store';
import { loadHistorySnapshot, pauseRunningTransfers } from './notif-bell';

const STORAGE_KEY = 'tdrive.transfers.v1';

// Bumped when a stored entry means something different. A record from another
// version is dropped rather than guessed at: the rows it would rebuild are the
// user's evidence of what happened, and a half-understood one is worse than an
// empty list.
const SNAPSHOT_VERSION = 1;

/**
 * How long a burst of changes may accumulate before it is written.
 *
 * A crash can only lose what happened inside one window, and what happens in
 * two seconds is a few percent of one transfer -- while writing on every change
 * would put a synchronous, disk-backed store call in the path of the progress
 * events. Backgrounding flushes immediately, so the window only ever applies to
 * a crash with the app on screen.
 */
const SAVE_INTERVAL_MS = 2_000;

/** Nothing the rows display needs more, and it bounds a tampered-with store. */
const MAX_TEXT_LENGTH = 300;

const TRANSFER_STATUSES: readonly TransferStatus[] = [
    'queued', 'active', 'paused', 'canceling', 'done', 'failed', 'canceled', 'stopped',
];

type HistoryStorage = Pick<Storage, 'getItem' | 'setItem' | 'removeItem'>;

interface StoredSnapshot {
    version: number;
    /** When the app last saw this work, which is as close as a restored row can get to when it stopped. */
    savedAt: number;
    events: Record<string, unknown>[];
}

let saveTimer: ReturnType<typeof setTimeout> | null = null;
let pendingSave = false;
let lastWritten: string | null = null;
let restoreAttempted = false;

/**
 * Starts keeping the log, and restores the last one on the first call.
 *
 * Restoring once per launch rather than once per activation: the dashboard is
 * mounted and unmounted again across a drive switch or a re-login, and a
 * second restore would resurrect rows the user had cleared in between.
 */
export function activateTransferPersistence(): () => void {
    if (!restoreAttempted) {
        restoreAttempted = true;
        const previous = readStoredHistory();
        if (previous.length > 0) loadHistorySnapshot(previous);
    }
    const unsubscribe = historyEvents.subscribe(scheduleSave);
    document.addEventListener('visibilitychange', handleVisibilityChange);
    // A phone webview is not always given a visibilitychange on the way out,
    // and pagehide is the one both hosts do raise when the page itself goes.
    window.addEventListener('pagehide', flushSave);
    return () => {
        document.removeEventListener('visibilitychange', handleVisibilityChange);
        window.removeEventListener('pagehide', flushSave);
        unsubscribe();
        flushSave();
    };
}

/** The stored log, already rested and bounded; empty whenever it cannot be trusted. */
export function readStoredHistory(): HistoryEvent[] {
    const store = storage();
    if (!store) return [];
    try {
        return restoredHistory(JSON.parse(store.getItem(STORAGE_KEY) ?? 'null'));
    } catch {
        // A truncated write or a hand-edited record is not worth a dead launch.
        return [];
    }
}

/**
 * What a stored record becomes when it is read back into a fresh process.
 *
 * Kept separate from the storage call so the rules it applies -- which statuses
 * a dead process leaves behind, how much of a record is allowed to exist -- can
 * be stated and tested without a Storage anywhere near them.
 */
export function restoredHistory(value: unknown): HistoryEvent[] {
    if (!value || typeof value !== 'object' || Array.isArray(value)) return [];
    const record = value as Partial<StoredSnapshot>;
    if (record.version !== SNAPSHOT_VERSION || !Array.isArray(record.events)) return [];
    const savedAt = count(record.savedAt);

    const seen = new Set<string>();
    const restored: HistoryEvent[] = [];
    for (const raw of record.events) {
        if (restored.length >= HISTORY_CAP) break;
        if (!raw || typeof raw !== 'object') continue;
        const stored = raw as Record<string, unknown>;
        const entry = stored.kind === 'transfer' ? restoredTransfer(stored, savedAt)
            : stored.kind === 'event' ? restoredNotice(stored)
            : null;
        // Every list that draws the history is keyed by id, and a keyed block
        // handed the same key twice throws. A store nobody validated does not
        // get to take the panel down with it.
        if (!entry || seen.has(entry.id)) continue;
        seen.add(entry.id);
        restored.push(entry);
    }
    return restored;
}

/** The record as it would be stored for these events at this moment. */
export function historySnapshot(events: readonly HistoryEvent[], savedAt: number): StoredSnapshot {
    return { version: SNAPSHOT_VERSION, savedAt, events: events.slice(0, HISTORY_CAP).map(storedEvent) };
}

function scheduleSave(): void {
    pendingSave = true;
    // The whole cost a progress tick is allowed to have. The timer is armed
    // once per burst, not once per change, so a transfer running flat out
    // still only reaches storage on the interval.
    if (saveTimer === null) saveTimer = setTimeout(runScheduledSave, SAVE_INTERVAL_MS);
}

function runScheduledSave(): void {
    saveTimer = null;
    flushSave();
}

function flushSave(): void {
    if (saveTimer !== null) clearTimeout(saveTimer);
    saveTimer = null;
    if (!pendingSave) return;
    pendingSave = false;
    writeStoredHistory(get(historyEvents));
}

function handleVisibilityChange(): void {
    if (document.visibilityState !== 'hidden') return;
    // iOS asks for no background modes (build/ios/Info.plist), so leaving the
    // screen suspends the whole process and not a byte moves until it comes
    // back. A row still drawing a live bar over that is the lie the paused
    // status exists to prevent, and it is also what the snapshot below would
    // otherwise record. Android keeps transferring behind its foreground
    // service, so its rows are left saying so.
    if (isIOSPlatform()) pauseRunningTransfers();
    // Last chance: after this the process may simply never be scheduled again.
    flushSave();
}

function writeStoredHistory(events: readonly HistoryEvent[]): void {
    const store = storage();
    if (!store) return;
    const snapshot = historySnapshot(events, Date.now());
    // The stamp is left out of the comparison so it cannot be the thing that
    // makes every record look new. Nothing volatile is in the rest of it, so a
    // transfer that is merely making progress compares equal to the bytes
    // already on disk and never reaches the store at all.
    const payload = JSON.stringify(snapshot.events);
    if (payload === lastWritten) return;
    try {
        if (snapshot.events.length === 0) store.removeItem(STORAGE_KEY);
        else store.setItem(STORAGE_KEY, JSON.stringify(snapshot));
        lastWritten = payload;
    } catch {
        // A full or read-only store costs the log, not the session. Leaving
        // lastWritten alone means the next flush tries again rather than
        // believing a write that never landed.
    }
}

function storedEvent(event: HistoryEvent): Record<string, unknown> {
    if (event.kind === 'transfer') {
        return {
            kind: 'transfer',
            id: event.id,
            direction: event.direction,
            name: event.name,
            // The size is the one figure kept, because Retry hands it straight
            // back to the download queue. Progress, bytes and speed are not:
            // see the header.
            total: event.total,
            status: event.status,
            startedAt: event.startedAt,
            finishedAt: event.finishedAt,
            ...(event.note ? { note: event.note } : {}),
        };
    }
    return {
        kind: 'event',
        id: event.id,
        level: event.level,
        title: event.title,
        body: event.body,
        ts: event.ts,
        ...(event.repeats && event.repeats > 1 ? { repeats: event.repeats } : {}),
    };
}

function restoredTransfer(stored: Record<string, unknown>, savedAt: number): TransferEvent | null {
    const id = text(stored.id);
    const direction: TransferDirection | null = stored.direction === 'up' || stored.direction === 'down'
        ? stored.direction
        : null;
    if (!id || !direction) return null;

    const previous = TRANSFER_STATUSES.includes(stored.status as TransferStatus)
        ? stored.status as TransferStatus
        : 'failed';
    const interrupted = isUnfinishedTransfer(previous);
    const status = restingStatus(previous);
    const startedAt = count(stored.startedAt);
    const note = status === 'failed' && interrupted ? interruptedNote(direction) : text(stored.note);
    return {
        kind: 'transfer',
        id,
        direction,
        name: text(stored.name),
        progress: 0,
        total: count(stored.total),
        bytes: 0,
        speed: 0,
        status,
        startedAt,
        // The app cannot know when it died, and the row wants to say how long
        // ago this was. The last time it was seen alive is the honest answer,
        // and it beats the alternative of a transfer that ended at the epoch.
        finishedAt: interrupted ? (savedAt || startedAt) : count(stored.finishedAt),
        ...(note ? { note } : {}),
    };
}

function restoredNotice(stored: Record<string, unknown>): NoticeEvent | null {
    const id = text(stored.id);
    if (!id) return null;
    const repeats = count(stored.repeats);
    return {
        kind: 'event',
        id,
        level: text(stored.level) || 'info',
        title: text(stored.title),
        body: text(stored.body),
        ts: count(stored.ts),
        ...(repeats > 1 ? { repeats } : {}),
    };
}

/**
 * What an unfinished transfer becomes once the process running it is gone.
 *
 * A transfer the user had already asked to stop is stopped now, and calling
 * that a failure would report an error for exactly the thing they wanted.
 * Everything else -- waiting, running, paused behind a backgrounded app --
 * ends the same way, because the file is not there. Failed is the honest word
 * for that, and it is also the one status the rows offer a way out of: it is
 * what puts Retry on a download row, and a download is re-offerable because
 * its message id is right there in the row's own key. An upload's source path
 * went with the process, so its row offers nothing and its note says why.
 */
function restingStatus(previous: TransferStatus): TransferStatus {
    if (previous === 'canceling') return 'canceled';
    return isUnfinishedTransfer(previous) ? 'failed' : previous;
}

/**
 * The one thing a restored row's status cannot say: that nothing went wrong
 * with the transfer itself, the app simply stopped existing underneath it.
 */
function interruptedNote(direction: TransferDirection): string {
    return direction === 'down'
        ? 'Interrupted when TDrive closed'
        : 'Interrupted when TDrive closed — choose the file again';
}

function storage(): HistoryStorage | null {
    try {
        return globalThis.localStorage ?? null;
    } catch {
        // Private mode and a locked-down webview throw on the access itself.
        return null;
    }
}

function text(value: unknown): string {
    return String(value ?? '').slice(0, MAX_TEXT_LENGTH);
}

function count(value: unknown): number {
    const parsed = Number(value);
    return Number.isFinite(parsed) && parsed > 0 ? Math.floor(parsed) : 0;
}
