// What the ongoing notification says, and how often the host is allowed to
// hear about it. The notification is a surface the user watches for minutes, so
// both are decided here rather than wherever a progress event happens to land.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const canRunInBackground = vi.hoisted(() => vi.fn(() => false));
const runInBackground = vi.hoisted(() => vi.fn(async () => {}));
const stopRunningInBackground = vi.hoisted(() => vi.fn(async () => {}));
vi.mock('../../modules/android-foreground', () => ({
    canRunInBackground,
    runInBackground,
    stopRunningInBackground,
}));

import { historyEvents, type TransferEvent } from '../notifications/notif-store';
import {
    activateBackgroundTransfers,
    describeTransfers,
    IDLE_BACKGROUND_STATE,
    planForegroundService,
    type BackgroundState,
} from './background-transfers';

function transfer(overrides: Partial<TransferEvent> = {}): TransferEvent {
    return {
        kind: 'transfer',
        id: 'xfer:up:1',
        direction: 'up',
        name: 'clip.mp4',
        progress: 0,
        total: 60_000_000,
        bytes: 0,
        speed: 500_000,
        status: 'active',
        startedAt: 0,
        finishedAt: 0,
        ...overrides,
    };
}

describe('what the notification says', () => {
    it('says nothing at all while the queue is empty', () => {
        expect(describeTransfers([])).toBeNull();
    });

    it('names the file when there is only one to name', () => {
        const notice = describeTransfers([transfer()]);
        expect(notice?.title).toBe('Uploading clip.mp4');
    });

    it('counts the files inside a folder rather than the folders they came in', () => {
        const notice = describeTransfers([
            transfer({ id: 'a', itemsTotal: 12 }),
            transfer({ id: 'b', name: 'notes.txt' }),
        ]);
        expect(notice?.title).toBe('Uploading 13 files');
    });

    it('says Transferring only when the queue is going both ways at once', () => {
        const notice = describeTransfers([
            transfer({ id: 'a' }),
            transfer({ id: 'b', direction: 'down', name: 'plan.pdf' }),
        ]);
        expect(notice?.title).toBe('Transferring 2 files');
    });

    it('estimates from everything the queue has left, not from its first file', () => {
        const notice = describeTransfers([transfer({ id: 'a' }), transfer({ id: 'b' })]);
        // 120 MB still to move at a combined 1 MB/s.
        expect(notice?.text).toContain('2 min left');
    });

    it('leaves a transfer whose size is unknown out of the totals', () => {
        const notice = describeTransfers([
            transfer({ id: 'a', total: 1000, bytes: 500, progress: 50 }),
            transfer({ id: 'b', total: 0, bytes: 9_999_999, progress: 0 }),
        ]);
        expect(notice?.progress).toBe(50);
    });

    it('leaves the bar indeterminate until something knows its size', () => {
        const notice = describeTransfers([
            transfer({ id: 'a', total: 0, speed: 0 }),
            transfer({ id: 'b', total: 0, speed: 0 }),
        ]);
        expect(notice?.progress).toBe(-1);
        expect(notice?.text).toBe('Preparing…');
    });

    it('gives a transfer waiting its turn an empty bar rather than a moving one', () => {
        const notice = describeTransfers([transfer({ status: 'queued' })]);
        expect(notice?.progress).toBe(0);
        expect(notice?.text).toBe('Waiting its turn');
    });
});

describe('when the host is told', () => {
    // Start the plan already running, as the interval and drain rules need.
    function running(now = 1000, queue = [transfer()]): BackgroundState {
        return planForegroundService(IDLE_BACKGROUND_STATE, queue, now).state;
    }

    it('starts the service the moment the first transfer appears', () => {
        const { state, plan } = planForegroundService(IDLE_BACKGROUND_STATE, [transfer()], 1000);
        expect(plan.notice?.title).toBe('Uploading clip.mp4');
        expect(state.running).toBe(true);
    });

    it('sends nothing when nothing about the transfer has changed', () => {
        const { plan } = planForegroundService(running(), [transfer()], 9000);
        expect(plan.notice).toBeNull();
        expect(plan.stop).toBe(false);
    });

    it('holds back an update that arrives faster than the line could usefully change', () => {
        const { plan } = planForegroundService(running(), [transfer({ progress: 3 })], 1400);
        expect(plan.notice).toBeNull();
        expect(plan.wakeAt).toBe(2500);
    });

    it('sends the held-back update once the interval has passed', () => {
        const { state, plan } = planForegroundService(running(), [transfer({ progress: 3 })], 2500);
        expect(plan.notice).not.toBeNull();
        expect(state.sentAt).toBe(2500);
    });

    it('lets a change of work through without waiting for the interval', () => {
        const { plan } = planForegroundService(running(), [transfer({ name: 'plan.pdf' })], 1100);
        expect(plan.notice?.title).toBe('Uploading plan.pdf');
    });

    it('keeps the service alive for a moment after the queue drains', () => {
        const { state, plan } = planForegroundService(running(), [], 2000);
        expect(plan.stop).toBe(false);
        expect(state.running).toBe(true);
        expect(plan.wakeAt).toBe(4500);
    });

    it('stops the service once the queue has stayed empty', () => {
        const drained = planForegroundService(running(), [], 2000).state;
        const { state, plan } = planForegroundService(drained, [], 4600);
        expect(plan.stop).toBe(true);
        expect(state.running).toBe(false);
    });

    it('calls off the pending stop when new work arrives inside the grace period', () => {
        const drained = planForegroundService(running(), [], 2000).state;
        const { state, plan } = planForegroundService(drained, [transfer()], 3000);
        expect(plan.stop).toBe(false);
        expect(state.idleSince).toBe(0);
    });

    it('never stops a service it did not start', () => {
        const { plan } = planForegroundService(IDLE_BACKGROUND_STATE, [], 1000);
        expect(plan.stop).toBe(false);
        expect(plan.notice).toBeNull();
    });
});

describe('watching the queue from the phone shell', () => {
    let dispose: () => void = () => {};

    beforeEach(() => {
        vi.useFakeTimers();
        historyEvents.set([]);
        canRunInBackground.mockReturnValue(false);
        runInBackground.mockClear();
        stopRunningInBackground.mockClear();
    });

    afterEach(() => {
        dispose();
        dispose = () => {};
        historyEvents.set([]);
        vi.useRealTimers();
    });

    it('stays out of the way where the host has no such service', () => {
        dispose = activateBackgroundTransfers();
        historyEvents.set([transfer()]);
        expect(runInBackground).not.toHaveBeenCalled();
    });

    it('holds the process open while there is a transfer, and lets it go after', () => {
        canRunInBackground.mockReturnValue(true);
        dispose = activateBackgroundTransfers();

        historyEvents.set([transfer()]);
        expect(runInBackground).toHaveBeenCalledOnce();

        historyEvents.set([{ ...transfer(), status: 'done', finishedAt: Date.now() }]);
        expect(stopRunningInBackground).not.toHaveBeenCalled();
        vi.advanceTimersByTime(3000);
        expect(stopRunningInBackground).toHaveBeenCalledOnce();
    });

    it('takes the notification down with the shell that raised it', () => {
        canRunInBackground.mockReturnValue(true);
        dispose = activateBackgroundTransfers();
        historyEvents.set([transfer()]);

        dispose();
        dispose = () => {};
        expect(stopRunningInBackground).toHaveBeenCalledOnce();
    });
});
