// Behavior tests for notification history, hover disclosure, auto-close, and
// terminal transfer invariants.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import NotifBell from '../ui/notifications/NotifBell.svelte';
import { get } from 'svelte/store';

const transferApi = vi.hoisted(() => ({
    cancelDownload: vi.fn(async () => undefined),
    cancelUpload: vi.fn(async () => undefined),
    cancelUploadById: vi.fn(async () => undefined),
}));

vi.mock('../api', async (importOriginal) => ({
    ...(await importOriginal<typeof import('../api')>()),
    ...transferApi,
}));
import {
    cancelTransfersInDirection,
    clearHistory,
    markTransferDone,
    cancelUploadFile,
    pauseRunningTransfers,
    pushQueuedTransfer,
    pushHistoryEvent,
    pushTransferStart,
    updateTransferProgress,
} from './notif-bell';
import {
    historyEvents,
    notifPanelOpen,
    notifUnreadErrors,
    type TransferEvent,
} from '../ui/notifications/notif-store';
import { downloadSharePaths, rememberDownloadSharePath } from '../ui/mobile/mobile-shell-store';
import { state } from '../state';


let host: HTMLElement;
let app: Record<string, unknown> | null = null;
/** Store notifications counted by the tests that assert on redraw noise. */
let writes = 0;

/** The transfer rows' statuses, newest first, with notices left out. */
function transferStatuses(): string[] {
    return get(historyEvents).filter((event) => event.kind === 'transfer').map((event) => event.status);
}

function bell(): HTMLElement {
    const el = document.getElementById('notif-bell');
    if (!el) throw new Error('bell not rendered');
    return el;
}

function reset(): void {
    notifPanelOpen.set(false);
    notifUnreadErrors.set(0);
    historyEvents.set([]);
    downloadSharePaths.set(new Map());
    state.activeDownloadId = null;
    state.cancelingUpload = false;
    writes = 0;
    flushSync();
}

beforeEach(() => {
    host = document.createElement('div');
    document.body.appendChild(host);
    app = mount(NotifBell, {
        target: host,
        props: {
            onCancelDirection: cancelTransfersInDirection,
            onCancelFile: cancelUploadFile,
            onClearHistory: clearHistory,
        },
    });
    flushSync();
});

afterEach(async () => {
    reset();
    if (app) await unmount(app);
    app = null;
    host.remove();
});

describe('notif-bell', () => {
    it('reflects transfer and error state in the bell mode', () => {
        expect(bell().dataset.mode).toBe('idle');

        pushTransferStart({ id: 1, direction: 'up', name: 'a.bin', total: 100 });
        flushSync();
        expect(bell().dataset.mode).toBe('active');

        markTransferDone({ id: 1, direction: 'up', status: 'failed' });
        flushSync();
        expect(bell().dataset.mode).toBe('error'); // unread error badge

        clearHistory();
        flushSync();
        expect(bell().dataset.mode).toBe('idle');
    });

    it('clears the log but not the work: anything unfinished survives Clear', () => {
        pushTransferStart({ id: 4, direction: 'up', name: 'running.bin', total: 10 });
        pushHistoryEvent({ level: 'info', title: 'Folder created' });
        // Queued and paused are part of the store's contract even though only
        // 'active' is published today; Clear must not be the thing that quietly
        // deletes them the day something does.
        historyEvents.update((events) => [
            {
                kind: 'transfer',
                id: 'xfer:down:file:9',
                direction: 'down',
                name: 'waiting.bin',
                progress: 0,
                total: 10,
                bytes: 0,
                speed: 0,
                status: 'queued',
                startedAt: 0,
                finishedAt: 0,
            },
            ...events,
        ]);
        markTransferDone({ id: 4, direction: 'up', status: 'done' });

        clearHistory();
        flushSync();

        const left = get(historyEvents);
        expect(left).toHaveLength(1);
        expect(left[0]).toMatchObject({ id: 'xfer:down:file:9', status: 'queued' });
    });

    it('clears iOS share paths along with terminal history', () => {
        pushTransferStart({ id: 9, direction: 'down', name: 'finished.pdf', total: 10 });
        markTransferDone({ id: 9, direction: 'down', status: 'done' });
        rememberDownloadSharePath('xfer:down:9', '/sandbox/Downloads/finished.pdf');

        clearHistory();

        expect(get(downloadSharePaths)).toEqual(new Map());
    });

    it('shows queued work before dispatch and promotes it in place', () => {
        pushQueuedTransfer({ id: 'file:12', direction: 'down', name: 'later.pdf', total: 100 });
        expect(get(historyEvents)[0]).toMatchObject({
            id: 'xfer:down:file:12',
            status: 'queued',
        });

        pushTransferStart({ id: 'file:12', direction: 'down', name: 'later.pdf', total: 100 });
        expect(get(historyEvents)).toHaveLength(1);
        expect(get(historyEvents)[0]).toMatchObject({ status: 'active' });
    });

    it('changes only the active download to Canceling before the backend replies', () => {
        pushTransferStart({ id: 'file:12', direction: 'down', name: 'first.pdf', total: 100 });
        pushQueuedTransfer({ id: 'file:13', direction: 'down', name: 'next.pdf', total: 100 });
        state.activeDownloadId = 'file:12';

        cancelTransfersInDirection('down');

        expect(get(historyEvents).filter((event) => event.kind === 'transfer')).toEqual([
            expect.objectContaining({ id: 'xfer:down:file:13', status: 'queued' }),
            expect.objectContaining({ id: 'xfer:down:file:12', status: 'canceling' }),
        ]);
        expect(transferApi.cancelDownload).toHaveBeenCalledOnce();
    });

    it('keeps terminal transfers immutable and byte math consistent', () => {
        pushTransferStart({ id: 2, direction: 'down', name: 'b.bin', total: 1000 });
        updateTransferProgress({ id: 2, direction: 'down', progress: 50 });

        let entry = get(historyEvents)[0] as TransferEvent;
        expect(entry.progress).toBe(50);
        expect(entry.bytes).toBe(500);

        markTransferDone({ id: 2, direction: 'down', status: 'canceled' });
        // A late 'done' from a safety sweep must not resurrect or upgrade it.
        markTransferDone({ id: 2, direction: 'down', status: 'done' });
        updateTransferProgress({ id: 2, direction: 'down', progress: 90 });

        entry = get(historyEvents)[0] as TransferEvent;
        expect(entry.status).toBe('canceled');
        expect(entry.progress).toBe(50);
    });

    it('tracks aggregate folder bytes and file counts monotonically', () => {
        pushTransferStart({ id: 'folder:d:project', direction: 'down', name: 'Project', total: 0 });
        updateTransferProgress({
            id: 'folder:d:project', direction: 'down', progress: 60,
            bytes: 600, total: 1000, itemsDone: 3, itemsTotal: 5,
        });
        // Retry callbacks can restart at zero. A stale update must not move any
        // visible aggregate counter backwards.
        updateTransferProgress({
            id: 'folder:d:project', direction: 'down', progress: 10,
            bytes: 100, total: 1000, itemsDone: 1, itemsTotal: 5,
        });
        flushSync();

        const entry = get(historyEvents)[0] as TransferEvent;
        expect(entry).toMatchObject({
            progress: 60,
            bytes: 600,
            total: 1000,
            itemsDone: 3,
            itemsTotal: 5,
        });
        bell().dispatchEvent(new MouseEvent('click', { bubbles: true }));
        flushSync();
        // The wording both rows share: the desktop popover stacks the figures
        // in its meta column and the phone joins the same ones into a line, but
        // ui/notifications/transfer-view decides what they say for both.
        expect(document.body.textContent).toContain('3 of 5 files');
        expect(document.body.textContent).toContain('600 of 1000 B');
    });

    it('writes the history only when a figure the row prints has moved', () => {
        vi.useFakeTimers();
        const stop = historyEvents.subscribe(() => { writes += 1; });
        try {
            pushTransferStart({ id: 'batch', direction: 'up', name: 'photos.zip', total: 100_000_000 });
            writes = 0;
            updateTransferProgress({ id: 'batch', direction: 'up', progress: 10, bytes: 10_000_000 });
            expect(writes).toBe(1);

            // Progress arrives tens of times a second. None of these move the
            // whole percent or the rounded size the row draws, and each write
            // would wake every derived store and the saver behind them.
            for (let tick = 1; tick <= 20; tick++) {
                vi.advanceTimersByTime(12);
                updateTransferProgress({
                    id: 'batch', direction: 'up',
                    progress: 10 + tick * 0.01, bytes: 10_000_000 + tick * 100,
                });
            }
            expect(writes).toBe(1);
            expect((get(historyEvents)[0] as TransferEvent).bytes).toBe(10_000_000);

            // The rate was sampled off every one of them even so: it is the
            // working behind "2 min left", and it would lurch if it only ever
            // saw the ticks that happened to be worth drawing.
            vi.advanceTimersByTime(1_000);
            updateTransferProgress({ id: 'batch', direction: 'up', progress: 12, bytes: 12_000_000 });
            expect(writes).toBe(2);
            expect((get(historyEvents)[0] as TransferEvent).speed).toBeGreaterThan(0);
        } finally {
            stop();
            vi.useRealTimers();
        }
    });

    it('lands on the true finished figures even where they would redraw the same', () => {
        pushTransferStart({ id: 5, direction: 'down', name: 'clip.mp4', total: 1_000_000_000 });
        updateTransferProgress({ id: 5, direction: 'down', progress: 99.96, bytes: 999_999_000 });
        const stop = historyEvents.subscribe(() => { writes += 1; });
        writes = 0;

        // Same whole percent and same "953.7 MB" as the tick before it, but
        // these are the figures markTransferDone freezes and the next launch
        // restores, so the last word has to be the true one.
        updateTransferProgress({ id: 5, direction: 'down', progress: 100, bytes: 1_000_000_000 });
        expect(writes).toBe(1);
        const entry = get(historyEvents)[0] as TransferEvent;
        expect([entry.progress, entry.bytes]).toEqual([100, 1_000_000_000]);

        // And says it once: a backend that keeps reporting 100% goes quiet again.
        updateTransferProgress({ id: 5, direction: 'down', progress: 100, bytes: 1_000_000_000 });
        expect(writes).toBe(1);
        stop();
    });

    it('gives a failed cancel back the rows it took, each to the status it had', async () => {
        // The iOS shape of it: the app is suspended, so what was running is
        // paused; the user comes back, taps Cancel all, and the RPC is refused.
        pushTransferStart({ id: 1, direction: 'up', name: 'running.bin', total: 10 });
        pushQueuedTransfer({ id: 2, direction: 'up', name: 'waiting.bin', total: 10 });
        pauseRunningTransfers();
        expect(transferStatuses()).toEqual(['queued', 'paused']);

        transferApi.cancelUpload.mockRejectedValueOnce(new Error('offline'));
        cancelTransfersInDirection('up');
        expect(transferStatuses()).toEqual(['canceling', 'canceling']);

        await vi.waitFor(() => expect(state.cancelingUpload).toBe(false));
        // Not 'active' for either: a paused row promoted to active draws a live
        // bar over a process moving no bytes, and a queued one claims a turn it
        // has not been given.
        expect(transferStatuses()).toEqual(['queued', 'paused']);
    });

    // Hover is an intent in both directions; NotifBell keeps the two delays equal.
    const HOVER_INTENT_MS = 140;

    it('opens the full panel on hover and closes after the pointer leaves', () => {
        vi.useFakeTimers();
        try {
            pushTransferStart({ id: 3, direction: 'up', name: 'c.bin', total: 10 });
            pushHistoryEvent({ level: 'error', title: 'Could not join drive', body: 'expired' });
            flushSync();
            expect(get(notifUnreadErrors)).toBe(1);

            bell().dispatchEvent(new MouseEvent('mouseenter'));
            // Opening waits out the same hover intent delay that closing does.
            vi.advanceTimersByTime(HOVER_INTENT_MS);
            flushSync();

            expect(get(notifPanelOpen)).toBe(true);
            expect(get(notifUnreadErrors)).toBe(0);
            const panel = document.body.querySelector<HTMLElement>('.notif-panel');
            expect(panel?.textContent).toContain('Active');
            expect(panel?.textContent).toContain('Recent');
            expect(panel?.textContent).toContain('Could not join drive');
            expect(panel?.querySelector('.notif-panel-close')).toBeNull();

            bell().dispatchEvent(new MouseEvent('mouseleave'));
            panel?.dispatchEvent(new MouseEvent('mouseenter'));
            vi.runOnlyPendingTimers();
            flushSync();
            expect(get(notifPanelOpen)).toBe(true);

            panel?.dispatchEvent(new MouseEvent('mouseleave'));
            vi.runOnlyPendingTimers();
            flushSync();
            expect(get(notifPanelOpen)).toBe(false);
        } finally {
            vi.useRealTimers();
        }
    });

    it('folds a repeated notice into the row already there', () => {
        // Tapping upload while an upload runs says the same sentence every
        // time. A list that repeats it verbatim buries whatever else is in it.
        pushHistoryEvent({ level: 'info', title: 'A transfer is already in progress', body: 'Wait for it to finish.' });
        pushHistoryEvent({ level: 'info', title: 'A transfer is already in progress', body: 'Wait for it to finish.' });
        pushHistoryEvent({ level: 'info', title: 'A transfer is already in progress', body: 'Wait for it to finish.' });

        const events = get(historyEvents);
        expect(events).toHaveLength(1);
        expect(events[0]).toMatchObject({ repeats: 3 });
    });

    it('only folds a run at the top, so history keeps its order', () => {
        // An older identical notice keeps its own place: what folds is a thing
        // happening twice in a row, not the same words appearing ever again.
        pushHistoryEvent({ level: 'info', title: 'Upload blocked', body: 'Busy.' });
        pushHistoryEvent({ level: 'success', title: 'Folder created', body: '' });
        pushHistoryEvent({ level: 'info', title: 'Upload blocked', body: 'Busy.' });

        const events = get(historyEvents);
        expect(events).toHaveLength(3);
        expect(events.every((event) => (event as { repeats?: number }).repeats === undefined)).toBe(true);
    });

    it('does not fold notices that only look alike', () => {
        pushHistoryEvent({ level: 'info', title: 'Upload blocked', body: 'Busy.' });
        pushHistoryEvent({ level: 'error', title: 'Upload blocked', body: 'Busy.' });

        expect(get(historyEvents)).toHaveLength(2);
    });

    it('caps history at 100 entries, newest first', () => {
        for (let i = 0; i < 120; i++) {
            pushHistoryEvent({ level: 'info', title: `event ${i}` });
        }
        const events = get(historyEvents);
        expect(events).toHaveLength(100);
        expect((events[0] as { title: string }).title).toBe('event 119');
    });
});
