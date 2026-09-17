// Behavior tests for the transfer log surviving a restart: what a restored row
// is allowed to claim, how little reaches storage while transfers are running,
// and what a backgrounded iOS app leaves behind.
//
// Every test gets its own copy of the module graph, because "restore once per
// launch" is a fact the module remembers and a fresh import is the only honest
// way to be a fresh launch.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { get } from 'svelte/store';
import type { HistoryEvent, NoticeEvent, TransferEvent } from '../ui/notifications/notif-store';

const platform = vi.hoisted(() => ({ ios: vi.fn(() => false) }));

vi.mock('../api', async (importOriginal) => ({
    ...(await importOriginal<typeof import('../api')>()),
    isIOSPlatform: platform.ios,
}));

const STORAGE_KEY = 'tdrive.transfers.v1';

let store: typeof import('../ui/notifications/notif-store');
let bell: typeof import('./notif-bell');
let persistence: typeof import('./transfer-persistence');

let stored: Map<string, string>;
let writes: number;
let refuseWrites: boolean;
let stop: (() => void) | null = null;

function useStorage(): void {
    stored = new Map();
    writes = 0;
    refuseWrites = false;
    vi.stubGlobal('localStorage', {
        getItem: (key: string) => stored.get(key) ?? null,
        setItem: (key: string, value: string) => {
            if (refuseWrites) throw new Error('quota exceeded');
            // Only this key is counted: other stores (the file list's sort, for
            // one) write themselves out as they are imported, and they are not
            // what these tests are measuring.
            if (key === STORAGE_KEY) writes += 1;
            stored.set(key, value);
        },
        removeItem: (key: string) => { stored.delete(key); },
    });
}

function onDisk(): { version?: number; savedAt?: number; events?: Record<string, unknown>[] } | null {
    const raw = stored.get(STORAGE_KEY);
    return raw ? JSON.parse(raw) : null;
}

function transfer(overrides: Partial<TransferEvent> = {}): TransferEvent {
    return {
        kind: 'transfer',
        id: 'xfer:down:file:101:5',
        direction: 'down',
        name: 'clip.mp4',
        progress: 62,
        total: 4_000,
        bytes: 2_480,
        speed: 900,
        status: 'active',
        startedAt: 1_000,
        finishedAt: 0,
        ...overrides,
    };
}

function notice(overrides: Partial<NoticeEvent> = {}): NoticeEvent {
    return { kind: 'event', id: 'evt:1', level: 'info', title: 'Folder created', body: '', ts: 2_000, ...overrides };
}

/** The record that would be on disk had the app been holding these events. */
function saved(events: HistoryEvent[], savedAt = 9_000): unknown {
    return persistence.historySnapshot(events, savedAt);
}

function restore(events: HistoryEvent[], savedAt = 9_000): HistoryEvent[] {
    return persistence.restoredHistory(saved(events, savedAt));
}

function lastRun(events: HistoryEvent[], savedAt = 9_000): void {
    stored.set(STORAGE_KEY, JSON.stringify(saved(events, savedAt)));
}

function logged(): string[] {
    return get(store.historyEvents).map((event) => event.id);
}

function transfers(): TransferEvent[] {
    return get(store.historyEvents).filter((event): event is TransferEvent => event.kind === 'transfer');
}

function setVisibility(value: 'hidden' | 'visible'): void {
    Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => value });
    document.dispatchEvent(new Event('visibilitychange'));
}

beforeEach(async () => {
    vi.useFakeTimers();
    platform.ios.mockReturnValue(false);
    useStorage();
    vi.resetModules();
    store = await import('../ui/notifications/notif-store');
    bell = await import('./notif-bell');
    persistence = await import('./transfer-persistence');
});

afterEach(() => {
    stop?.();
    stop = null;
    setVisibility('visible');
    vi.useRealTimers();
    vi.unstubAllGlobals();
});

describe('restoring the transfer log', () => {
    it('brings a transfer that was still running back as failed, because whatever was running it is gone', () => {
        const [restored] = restore([transfer({ status: 'active' })]) as TransferEvent[];
        expect(restored.status).toBe('failed');
        expect(restored.name).toBe('clip.mp4');
        // The size survives because Retry hands it back to the download queue.
        // How far it had got does not: the next attempt starts from nothing.
        expect(restored.total).toBe(4_000);
        expect([restored.progress, restored.bytes, restored.speed]).toEqual([0, 0, 0]);
    });

    it('rests work that was only waiting its turn the same way as work already moving', () => {
        for (const status of ['queued', 'active', 'paused'] as const) {
            expect(restore([transfer({ status })])[0]).toMatchObject({ status: 'failed' });
        }
    });

    it('tells an interrupted upload apart from a download, since only one of them can be offered again', () => {
        const [download] = restore([transfer({ direction: 'down' })]) as TransferEvent[];
        const [upload] = restore([transfer({ id: 'xfer:up:3', direction: 'up' })]) as TransferEvent[];
        expect(download.note).toBe('Interrupted when TDrive closed');
        expect(upload.note).toBe('Interrupted when TDrive closed — choose the file again');
    });

    it('reports a cancel that was still in flight as canceled rather than as a failure', () => {
        const [restored] = restore([transfer({ status: 'canceling' })]) as TransferEvent[];
        expect(restored.status).toBe('canceled');
        // Stopping was what the user asked for, so the row has nothing to explain.
        expect(restored.note).toBeUndefined();
    });

    it('dates an interrupted transfer to the last time the app saw it', () => {
        expect(restore([transfer()], 9_000)[0]).toMatchObject({ finishedAt: 9_000 });
        // A record too old to carry the stamp still beats a row that claims to
        // have ended at the epoch.
        expect(restore([transfer({ startedAt: 4_000 })], 0)[0]).toMatchObject({ finishedAt: 4_000 });
    });

    it('keeps a finished transfer’s outcome, its time and the place it landed', () => {
        const done = transfer({ status: 'done', finishedAt: 5_000, note: 'Saved to Files › TDrive › Downloads' });
        expect(restore([done])[0]).toMatchObject({
            status: 'done',
            finishedAt: 5_000,
            note: 'Saved to Files › TDrive › Downloads',
        });
    });

    it('keeps the notices in the log too, including how often one of them repeated', () => {
        const [restored] = restore([notice({ repeats: 3, body: 'Photos' })]) as NoticeEvent[];
        expect(restored).toMatchObject({ kind: 'event', title: 'Folder created', body: 'Photos', repeats: 3 });
        expect(restore([notice()])[0]).not.toHaveProperty('repeats');
    });
});

describe('reading a stored log that cannot be trusted', () => {
    it('ignores a record written by another version of the app', () => {
        const record = saved([transfer()]) as { version: number };
        expect(persistence.restoredHistory({ ...record, version: 99 })).toEqual([]);
        expect(persistence.restoredHistory({ ...record, version: undefined })).toEqual([]);
    });

    it('ignores anything that is not a record it wrote', () => {
        for (const value of [null, undefined, 42, 'transfers', [transfer()], {}, { version: 1 }]) {
            expect(persistence.restoredHistory(value)).toEqual([]);
        }
    });

    it('drops entries it could not draw, and never hands back the same id twice', () => {
        const record = {
            version: 1,
            savedAt: 9_000,
            events: [
                { kind: 'transfer', id: 'xfer:down:file:101:5', direction: 'down', status: 'done' },
                // A repeated id takes down every keyed list that draws the history.
                { kind: 'transfer', id: 'xfer:down:file:101:5', direction: 'down', status: 'done' },
                { kind: 'transfer', id: '', direction: 'down' },
                { kind: 'transfer', id: 'xfer:sideways:1', direction: 'sideways' },
                { kind: 'sandwich', id: 'lunch' },
                null,
                'nonsense',
            ],
        };
        expect(persistence.restoredHistory(record).map((event) => event.id)).toEqual(['xfer:down:file:101:5']);
    });

    it('replaces unusable fields rather than trusting them into the rows', () => {
        const [restored] = persistence.restoredHistory({
            version: 1,
            savedAt: 9_000,
            events: [{
                kind: 'transfer', id: 'xfer:up:1', direction: 'up',
                name: 'x'.repeat(5_000), total: -8, status: 'imaginary', startedAt: NaN,
            }],
        }) as TransferEvent[];
        expect(restored.name.length).toBe(300);
        expect(restored.total).toBe(0);
        expect(restored.startedAt).toBe(0);
        expect(restored.status).toBe('failed');
    });

    it('never hands back more entries than the history keeps', () => {
        const many = Array.from({ length: 250 }, (_, index) => ({
            kind: 'transfer', id: `xfer:up:${index}`, direction: 'up', status: 'done',
        }));
        expect(persistence.restoredHistory({ version: 1, savedAt: 9_000, events: many })).toHaveLength(100);
    });

    it('survives unreadable, unparseable and absent storage', () => {
        stored.set(STORAGE_KEY, '{not json');
        expect(persistence.readStoredHistory()).toEqual([]);
        vi.stubGlobal('localStorage', { getItem: () => { throw new Error('denied'); } });
        expect(persistence.readStoredHistory()).toEqual([]);
        vi.stubGlobal('localStorage', undefined);
        expect(persistence.readStoredHistory()).toEqual([]);
        expect(() => persistence.activateTransferPersistence()()).not.toThrow();
    });
});

describe('writing the transfer log', () => {
    it('folds a burst of changes into one write', () => {
        stop = persistence.activateTransferPersistence();
        bell.pushTransferStart({ id: 1, direction: 'up', name: 'a.bin', total: 10 });
        bell.pushTransferStart({ id: 2, direction: 'up', name: 'b.bin', total: 20 });
        bell.pushHistoryEvent({ level: 'info', title: 'Folder created' });
        expect(writes).toBe(0);

        vi.advanceTimersByTime(2_000);
        expect(writes).toBe(1);
        expect(onDisk()?.events).toHaveLength(3);
    });

    it('writes nothing at all while a transfer is only making progress', () => {
        stop = persistence.activateTransferPersistence();
        bell.pushTransferStart({ id: 1, direction: 'up', name: 'a.bin', total: 1_000 });
        vi.advanceTimersByTime(2_000);
        expect(writes).toBe(1);

        for (let percent = 1; percent <= 100; percent += 1) {
            bell.updateTransferProgress({ id: 1, direction: 'up', progress: percent });
        }
        vi.advanceTimersByTime(20_000);
        expect(writes).toBe(1);

        // Only an outcome is worth the write.
        bell.markTransferDone({ id: 1, direction: 'up', status: 'done' });
        vi.advanceTimersByTime(2_000);
        expect(writes).toBe(2);
        expect(onDisk()?.events?.[0]).toMatchObject({ status: 'done' });
    });

    it('keeps going when the store refuses the write, and tries again on the next change', () => {
        stop = persistence.activateTransferPersistence();
        refuseWrites = true;
        bell.pushTransferStart({ id: 1, direction: 'up', name: 'a.bin', total: 10 });
        vi.advanceTimersByTime(2_000);
        expect(onDisk()).toBeNull();

        refuseWrites = false;
        bell.pushTransferStart({ id: 2, direction: 'up', name: 'b.bin', total: 10 });
        vi.advanceTimersByTime(2_000);
        expect(onDisk()?.events).toHaveLength(2);
    });

    it('clears the stored record once there is nothing left to remember', () => {
        stop = persistence.activateTransferPersistence();
        bell.pushTransferStart({ id: 1, direction: 'up', name: 'a.bin', total: 10 });
        bell.markTransferDone({ id: 1, direction: 'up', status: 'done' });
        vi.advanceTimersByTime(2_000);
        expect(onDisk()).not.toBeNull();

        bell.clearHistory();
        vi.advanceTimersByTime(2_000);
        expect(onDisk()).toBeNull();
    });

    it('saves what is pending on the way out rather than losing the last window', () => {
        stop = persistence.activateTransferPersistence();
        bell.pushTransferStart({ id: 1, direction: 'up', name: 'a.bin', total: 10 });
        stop();
        stop = null;
        expect(onDisk()?.events).toHaveLength(1);
    });
});

describe('leaving the screen', () => {
    it('pauses what is running and saves at once when an iOS app is backgrounded', () => {
        platform.ios.mockReturnValue(true);
        stop = persistence.activateTransferPersistence();
        bell.pushTransferStart({ id: 7, direction: 'down', name: 'clip.mp4', total: 100 });

        setVisibility('hidden');
        expect(transfers()[0].status).toBe('paused');
        // Saved without waiting out the window: iOS may never schedule this
        // process again.
        expect(writes).toBe(1);
        expect(onDisk()?.events?.[0]).toMatchObject({ status: 'paused' });
    });

    it('leaves the rows running where the platform keeps transferring in the background', () => {
        stop = persistence.activateTransferPersistence();
        bell.pushTransferStart({ id: 7, direction: 'down', name: 'clip.mp4', total: 100 });

        setVisibility('hidden');
        expect(transfers()[0].status).toBe('active');
        expect(writes).toBe(1);
    });

    it('lifts a paused transfer back out of paused only once its bytes move again', () => {
        platform.ios.mockReturnValue(true);
        stop = persistence.activateTransferPersistence();
        bell.pushTransferStart({ id: 7, direction: 'down', name: 'clip.mp4', total: 100 });
        bell.updateTransferProgress({ id: 7, direction: 'down', progress: 40 });

        setVisibility('hidden');
        setVisibility('visible');
        expect(transfers()[0].status).toBe('paused');

        // Even a tick that repeats the last figure counts: it is the proof that
        // something on the other end is still there.
        bell.updateTransferProgress({ id: 7, direction: 'down', progress: 40 });
        expect(transfers()[0].status).toBe('active');
    });
});

describe('starting up', () => {
    it('restores the last run’s log into an empty bell', () => {
        lastRun([transfer(), notice()]);
        stop = persistence.activateTransferPersistence();
        expect(logged()).toEqual(['xfer:down:file:101:5', 'evt:1']);
        expect(transfers()[0].status).toBe('failed');
    });

    it('files the restored log under work this session has already started, never over it', () => {
        lastRun([transfer(), transfer({ id: 'xfer:up:5', direction: 'up', name: 'stale.bin' })]);
        bell.pushTransferStart({ id: 5, direction: 'up', name: 'now.bin', total: 1 });

        stop = persistence.activateTransferPersistence();
        expect(logged()).toEqual(['xfer:up:5', 'xfer:down:file:101:5']);
        // The live row kept its own state; the dead copy of it was dropped.
        expect(transfers()[0]).toMatchObject({ name: 'now.bin', status: 'active' });
    });

    it('restores once per launch, however often the surfaces are rebuilt', () => {
        lastRun([transfer()]);
        persistence.activateTransferPersistence()();
        bell.clearHistory();

        stop = persistence.activateTransferPersistence();
        expect(logged()).toEqual([]);
    });
});
