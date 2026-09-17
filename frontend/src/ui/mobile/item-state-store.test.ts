import { get } from 'svelte/store';
import { state } from '../../state';
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
    it('only maps a drive-scoped download to rows in its source drive', () => {
        state.activeChannel = { id: 7, title: 'Drive A', kind: 'personal' };
        historyEvents.set([transfer({ id: 'xfer:down:file:7:1042' })]);
        const transfers = get(transfersByFile);
        expect(transfers.get('7:1042')?.id).toBe('xfer:down:file:7:1042');

        state.activeChannel = { id: 8, title: 'Drive B', kind: 'shared' };
        expect(itemStateFor(transfers, '1042')).toBe('online-only');
    });

    it('keys a download by the row id its queue key names', () => {
        // The download queue keys jobs "file:<id>"; the row carries "<id>".
        // These have to meet or a download never badges the file it is for.
        historyEvents.set([transfer({ id: 'xfer:down:file:1042' })]);
        expect(get(transfersByFile).get('1042')?.id).toBe('xfer:down:file:1042');
    });

    it('keeps a folder id whole', () => {
        historyEvents.set([transfer({ id: 'xfer:down:folder:d:design' })]);
        expect(get(transfersByFile).get('d:design')?.direction).toBe('down');
    });

    it('keeps the newest transfer when a file has more than one', () => {
        historyEvents.set([
            transfer({ id: 'xfer:down:file:7', status: 'active', direction: 'down' }),
            transfer({ id: 'xfer:up:file:7', status: 'done', direction: 'up' }),
        ]);
        expect(get(transfersByFile).get('7')?.direction).toBe('down');
    });

    it('ignores notices and keys that name no file', () => {
        // "xfer:up:3" is the fourth file of an upload batch, not the file with
        // id 3 -- badging that row would mark a stranger's file failed.
        historyEvents.set([
            { kind: 'event', id: 'n1', level: 'info', title: 'hi', body: '', ts: 0 },
            transfer({ id: 'nonsense' }),
            transfer({ id: 'xfer:up:3', status: 'failed', direction: 'up' }),
            transfer({ id: 'xfer:up:import', status: 'active', direction: 'up' }),
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
        historyEvents.set([transfer({ id: 'xfer:down:file:9', status: 'active', direction: 'down' })]);
        expect(itemStateFor(get(transfersByFile), '9')).toBe('downloading');
        historyEvents.set([transfer({ id: 'xfer:up:file:9', status: 'active', direction: 'up' })]);
        expect(itemStateFor(get(transfersByFile), '9')).toBe('syncing');
    });

    it('drops a cancelled transfer back to the stored state', () => {
        // Cancelling undoes the intent, so the row should stop advertising it
        // rather than keep a badge for work the user called off.
        historyEvents.set([transfer({ id: 'xfer:down:file:4', status: 'canceled' })]);
        expect(itemStateFor(get(transfersByFile), '4', { offline: true })).toBe('available-offline');
        expect(itemStateFor(get(transfersByFile), '4')).toBe('online-only');
    });

    it('lets a failure outrank a pinned local copy', () => {
        historyEvents.set([transfer({ id: 'xfer:down:file:5', status: 'failed', direction: 'down' })]);
        expect(itemStateFor(get(transfersByFile), '5', { offline: true })).toBe('failed');
    });
});
