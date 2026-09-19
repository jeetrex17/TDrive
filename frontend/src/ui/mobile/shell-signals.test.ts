// The two ambient signals answer different questions, and this is where that
// separation is pinned down: the ring says whether the app is doing work, the
// badge says whether anything is waiting for a person.
import { get } from 'svelte/store';
import { beforeEach, describe, expect, it } from 'vitest';
import { historyEvents, type TransferEvent } from '../notifications/notif-store';
import { driveSyncStatus, markTransfersSeen, ringState, transferAttentionCount, transfersSeenAt } from './mobile-shell-store';

function transfer(status: TransferEvent['status'], id = `xfer:up:${status}`): TransferEvent {
    return {
        kind: 'transfer',
        id,
        direction: 'up',
        name: 'clip.mp4',
        progress: 0,
        total: 0,
        bytes: 0,
        speed: 0,
        status,
        startedAt: 0,
        finishedAt: 0,
    };
}

beforeEach(() => {
    historyEvents.set([]);
    driveSyncStatus.set('idle');
    transfersSeenAt.set(0);
});

describe('transferAttentionCount', () => {
    it('counts only what needs a person', () => {
        historyEvents.set([
            transfer('active', 'xfer:up:1'),
            transfer('queued', 'xfer:up:2'),
            transfer('done', 'xfer:up:3'),
            transfer('canceled', 'xfer:up:4'),
        ]);
        // Nothing here is waiting on the user, so the badge stays off even
        // though four transfers exist.
        expect(get(transferAttentionCount)).toBe(0);
    });

    it('counts failures', () => {
        historyEvents.set([transfer('failed', 'xfer:up:1'), transfer('failed', 'xfer:down:2'), transfer('active', 'xfer:up:3')]);
        expect(get(transferAttentionCount)).toBe(2);
    });

    // A badge is an interrupt, and looking is what answers it. Counting every
    // failure in a history that persists kept the badge lit across launches
    // until the whole list was cleared.
    it('is answered by looking at the tab, and lit again only by a newer failure', () => {
        historyEvents.set([{ ...transfer('failed', 'xfer:up:1'), finishedAt: 1_000 }]);
        expect(get(transferAttentionCount)).toBe(1);
        markTransfersSeen();
        expect(get(transferAttentionCount)).toBe(0);
        historyEvents.set([
            { ...transfer('failed', 'xfer:up:1'), finishedAt: 1_000 },
            { ...transfer('failed', 'xfer:up:2'), finishedAt: Date.now() + 1 },
        ]);
        expect(get(transferAttentionCount)).toBe(1);
    });
});

describe('ringState', () => {
    it('rests when nothing is happening', () => {
        expect(get(ringState)).toBe('idle');
    });

    it('turns active for either kind of work', () => {
        driveSyncStatus.set('syncing');
        expect(get(ringState)).toBe('active');

        driveSyncStatus.set('idle');
        historyEvents.set([transfer('active')]);
        expect(get(ringState)).toBe('active');
    });

    it('keeps a failure visible while other work carries on', () => {
        // The broken thing is the part the user can act on, so progress must
        // not paint over it.
        historyEvents.set([transfer('failed', 'xfer:up:1'), transfer('active', 'xfer:up:2')]);
        expect(get(ringState)).toBe('attention');
    });

    it('puts a failed drive sync above a failed transfer', () => {
        driveSyncStatus.set('failed');
        historyEvents.set([transfer('failed')]);
        expect(get(ringState)).toBe('failed');
    });

    it('does not light up for finished work', () => {
        historyEvents.set([transfer('done'), transfer('canceled', 'xfer:up:9')]);
        expect(get(ringState)).toBe('idle');
    });
});
