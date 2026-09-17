// Which cancel a row gets is a property of the queue, not of the row, so it is
// decided in the tab and asserted here.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';

const cancelSingleUpload = vi.hoisted(() => vi.fn());
const cancelTransfersInDirection = vi.hoisted(() => vi.fn());
vi.mock('../../modules/notif-bell', async (importOriginal) => {
    const actual = await importOriginal<typeof import('../../modules/notif-bell')>();
    return { ...actual, cancelSingleUpload, cancelTransfersInDirection };
});

import TransfersTab from './TransfersTab.svelte';
import { historyEvents, type TransferEvent } from '../notifications/notif-store';

let host: HTMLElement;
let app: Record<string, unknown> | null = null;

function transfer(overrides: Partial<TransferEvent> = {}): TransferEvent {
    return {
        kind: 'transfer',
        id: 'xfer:up:1',
        direction: 'up',
        name: 'clip.mp4',
        progress: 40,
        total: 1000,
        bytes: 400,
        speed: 0,
        status: 'active',
        startedAt: 0,
        finishedAt: 0,
        ...overrides,
    };
}

function render(queue: TransferEvent[]): void {
    historyEvents.set(queue);
    app = mount(TransfersTab, { target: host, props: {} });
    flushSync();
}

function clickStop(name: string): void {
    const button = host.querySelector<HTMLButtonElement>(`button[aria-label="Stop ${name}"]`);
    if (!button) throw new Error(`No stop control for ${name}`);
    button.click();
}

beforeEach(() => {
    cancelSingleUpload.mockClear();
    cancelTransfersInDirection.mockClear();
    host = document.createElement('div');
    document.body.append(host);
});

afterEach(async () => {
    if (app) await unmount(app);
    app = null;
    historyEvents.set([]);
    host.remove();
});

describe('stopping one transfer', () => {
    it('stops just that upload, because the tab also offers Cancel all', () => {
        render([transfer({ id: 'xfer:up:1' })]);
        clickStop('clip.mp4');
        expect(cancelSingleUpload).toHaveBeenCalledExactlyOnceWith(1);
        expect(cancelTransfersInDirection).not.toHaveBeenCalled();
    });

    it('falls back to the whole direction for an import, which has no per-file id', () => {
        render([transfer({ id: 'xfer:up:import' })]);
        clickStop('clip.mp4');
        expect(cancelTransfersInDirection).toHaveBeenCalledExactlyOnceWith('up');
        expect(cancelSingleUpload).not.toHaveBeenCalled();
    });

    it('stops the download that is actually running', () => {
        render([transfer({ id: 'xfer:down:file:42', direction: 'down', name: 'plan.pdf' })]);
        clickStop('plan.pdf');
        expect(cancelTransfersInDirection).toHaveBeenCalledExactlyOnceWith('down');
    });

    it('does not pretend a waiting download can be stopped on its own', () => {
        render([transfer({ id: 'xfer:down:file:42', direction: 'down', name: 'plan.pdf', status: 'queued' })]);
        expect(host.querySelector('button[aria-label="Stop plan.pdf"]')).toBeNull();
    });
});

describe('stopping the lot', () => {
    it('is offered only once there is more than one transfer to stop', () => {
        render([transfer()]);
        expect(host.querySelector('button.transfers-clear')).toBeNull();
    });

    it('stops both directions, since a queue can be waiting in either', () => {
        render([
            transfer({ id: 'xfer:up:1' }),
            transfer({ id: 'xfer:down:file:42', direction: 'down', name: 'plan.pdf', status: 'queued' }),
        ]);
        const button = host.querySelector<HTMLButtonElement>('button.transfers-clear');
        expect(button?.textContent?.trim()).toBe('Cancel all');
        button?.click();
        expect(cancelTransfersInDirection).toHaveBeenCalledTimes(2);
        expect(cancelTransfersInDirection).toHaveBeenCalledWith('up');
        expect(cancelTransfersInDirection).toHaveBeenCalledWith('down');
    });
});

describe('a download that has not worked out its size yet', () => {
    it('can still be stopped: it is the job the backend has in hand', () => {
        render([transfer({
            id: 'xfer:down:file:42', direction: 'down', name: 'plan.pdf',
            status: 'active', progress: 0, total: 0, bytes: 0,
        })]);
        expect(host.textContent).toContain('Preparing');
        clickStop('plan.pdf');
        expect(cancelTransfersInDirection).toHaveBeenCalledExactlyOnceWith('down');
    });
});
