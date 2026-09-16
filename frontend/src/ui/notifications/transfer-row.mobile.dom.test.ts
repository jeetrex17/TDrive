// The transfer row is shared with the desktop bell, so everything the phone
// does differently is gated on the platform. These cover the phone half; the
// desktop spellings are asserted in modules/notif-bell.dom.test.ts.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount, type ComponentProps } from 'svelte';
import TransferRow from './TransferRow.svelte';
import type { TransferEvent, TransferStatus } from './notif-store';

vi.mock('../../api', async (importOriginal) => {
    const actual = await importOriginal<typeof import('../../api')>();
    return { ...actual, isMobilePlatform: () => true };
});

let host: HTMLElement;
let app: Record<string, unknown> | null = null;

function transfer(overrides: Partial<TransferEvent> = {}): TransferEvent {
    return {
        kind: 'transfer',
        id: 'xfer:down:file:42',
        direction: 'down',
        name: 'plan.pdf',
        progress: 60,
        total: 1000,
        bytes: 600,
        speed: 0,
        status: 'active' as TransferStatus,
        startedAt: 0,
        finishedAt: 0,
        ...overrides,
    };
}

type RowProps = ComponentProps<typeof TransferRow>;

function render(props: RowProps): void {
    app = mount(TransferRow, { target: host, props });
    flushSync();
}

beforeEach(() => {
    host = document.createElement('div');
    document.body.append(host);
});

afterEach(async () => {
    if (app) await unmount(app);
    app = null;
    host.remove();
});

describe('transfer row on a phone', () => {
    it('names the unit once when both halves share it', () => {
        render({ transfer: transfer() });
        expect(host.textContent).toContain('600 / 1000 B');
        expect(host.textContent).not.toContain('600 B / 1000 B');
    });

    it('spells out a file count instead of stacking two slashes on one line', () => {
        render({ transfer: transfer({ itemsDone: 3, itemsTotal: 5 }) });
        expect(host.textContent).toContain('3 of 5 files');
    });

    it('offers cancel for work that has not finished, whether or not it is moving', () => {
        render({ transfer: transfer({ status: 'queued' }), onCancel: () => {} });
        expect(host.querySelector('.notif-row-cancel')).not.toBeNull();
    });

    it('says when the one cancel handle will take other transfers with it', () => {
        render({ transfer: transfer(), onCancel: () => {}, cancelLabel: 'Cancel all 3 downloads' });
        const button = host.querySelector('.notif-row-cancel');
        expect(button?.getAttribute('aria-label')).toBe('Cancel all 3 downloads');
    });

    it('gives a failed download a way back, and calls it once tapped', () => {
        const onRetry = vi.fn();
        render({ transfer: transfer({ status: 'failed' }), onRetry });
        const button = host.querySelector<HTMLButtonElement>('.notif-row-retry');
        expect(button).not.toBeNull();
        button?.click();
        expect(onRetry).toHaveBeenCalledTimes(1);
    });

    it('leaves a failed upload alone, because there is no retry that could work', () => {
        render({ transfer: transfer({ status: 'failed', direction: 'up', id: 'xfer:up:2' }) });
        expect(host.querySelector('.notif-row-retry')).toBeNull();
        expect(host.querySelector('.notif-row-cancel')).toBeNull();
        expect(host.textContent).toContain('Failed');
    });

    it('marks a failed row as failed rather than as still running', () => {
        render({ transfer: transfer({ status: 'failed' }) });
        expect(host.querySelector('.notif-row-transfer.is-failed')).not.toBeNull();
        expect(host.querySelector('.notif-row-transfer.is-active')).toBeNull();
    });
});
