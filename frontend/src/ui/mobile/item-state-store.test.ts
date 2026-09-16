import { get } from 'svelte/store';
import { beforeEach, describe, expect, it } from 'vitest';
import { historyEvents, type TransferEvent } from '../notifications/notif-store';
import { itemStateFor, transfersByFile } from './item-state-store';

function transfer(overrides: Partial<TransferEvent> & Pick<TransferEvent, 'id'>): TransferEvent {
    return {
        kind: 'transfer',
        direction: 'down',
        name: 'plan.pdf',
        progress: 0,
        total: 0,
        bytes: 0,
        speed: 0,
        status: 'active',
        startedAt: 0,
        finishedAt: 0,
        ...overrides,
    };
}

beforeEach(() => historyEvents.set([]));

describe('transfersByFile', () => {
    it('keys transfers by the file id inside their transfer key', () => {
        historyEvents.set([transfer({ id: 'xfer:down:1042' })]);
        expect(get(transfersByFile).get('1042')?.id).toBe('xfer:down:1042');
    });

    it('keeps the newest transfer when a file has more than one', () => {
        historyEvents.set([
            transfer({ id: 'xfer:down:7', status: 'active', direction: 'down' }),
            transfer({ id: 'xfer:up:7', status: 'done', direction: 'up' }),
        ]);
        expect(get(transfersByFile).get('7')?.direction).toBe('down');
    });

    it('ignores notices and malformed keys', () => {
        historyEvents.set([
            { kind: 'event', id: 'n1', level: 'info', title: 'hi', body: '', ts: 0 },
            transfer({ id: 'nonsense' }),
        ]);
        expect(get(transfersByFile).size).toBe(0);
    });
});

describe('itemStateFor', () => {
    it('reports stored state for a file nothing is doing', () => {
        const none = new Map<string, TransferEvent>();
        expect(itemStateFor(none, '1')).toBe('online-only');
        expect(itemStateFor(none, '1', { offline: true })).toBe('available-offline');
        expect(itemStateFor(none, '1', { conflicted: true })).toBe('conflict');
    });

    it('reads the live transfer when there is one', () => {
        historyEvents.set([transfer({ id: 'xfer:up:9', status: 'active', direction: 'up' })]);
        expect(itemStateFor(get(transfersByFile), '9')).toBe('syncing');
    });

    it('drops a cancelled transfer back to the stored state', () => {
        // Cancelling undoes the intent, so the row should stop advertising it
        // rather than keep a badge for work the user called off.
        historyEvents.set([transfer({ id: 'xfer:down:4', status: 'canceled' })]);
        expect(itemStateFor(get(transfersByFile), '4', { offline: true })).toBe('available-offline');
        expect(itemStateFor(get(transfersByFile), '4')).toBe('online-only');
    });

    it('lets a failure outrank a pinned local copy', () => {
        historyEvents.set([transfer({ id: 'xfer:up:5', status: 'failed', direction: 'up' })]);
        expect(itemStateFor(get(transfersByFile), '5', { offline: true })).toBe('failed');
    });
});
