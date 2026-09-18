// The phone's own transfer row. What each state is *called* is covered in
// ui/notifications/transfer-view.test.ts; this covers what gets drawn and which
// single control a row offers.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount, type ComponentProps } from 'svelte';
import MobileTransferRow from './MobileTransferRow.svelte';
import type { TransferEvent } from '../notifications/notif-store';

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
        status: 'active',
        startedAt: 0,
        finishedAt: 0,
        ...overrides,
    };
}

function render(props: ComponentProps<typeof MobileTransferRow>): void {
    app = mount(MobileTransferRow, { target: host, props });
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

describe('what the row draws', () => {
    it('gives a moving transfer a bar and reports its position without a live region', () => {
        render({ transfer: transfer() });
        const bar = host.querySelector('[role="progressbar"]');
        expect(bar?.getAttribute('aria-valuenow')).toBe('60');
        expect(bar?.getAttribute('aria-label')).toContain('Downloading plan.pdf');
        expect(bar?.hasAttribute('aria-live')).toBe(false);
    });

    it('says a queued transfer is waiting instead of drawing it as live at 0%', () => {
        render({ transfer: transfer({ status: 'queued', progress: 0, bytes: 0 }) });
        expect(host.textContent).toContain('Waiting its turn');
        expect(host.querySelector('[role="progressbar"]')).not.toBeNull();
    });

    it('leaves the bar indeterminate while there is nothing to measure', () => {
        render({ transfer: transfer({ total: 0, bytes: 0, progress: 0 }) });
        const bar = host.querySelector('[role="progressbar"]');
        expect(host.textContent).toContain('Preparing');
        expect(bar?.getAttribute('aria-valuenow')).toBeNull();
        expect(bar?.classList.contains('is-indeterminate')).toBe(true);
    });

    it('drops the bar once a transfer has stopped, and dates what happened', () => {
        render({ transfer: transfer({ status: 'done', progress: 100, finishedAt: Date.now() - 120_000 }) });
        expect(host.querySelector('[role="progressbar"]')).toBeNull();
        expect(host.textContent).toContain('Done · 2 min ago');
    });

    it('marks a failed row as failed rather than as still running', () => {
        render({ transfer: transfer({ status: 'failed' }) });
        expect(host.querySelector('[data-phase="failed"]')).not.toBeNull();
        expect(host.textContent).toContain('Failed');
    });
});

describe('the one control a row offers', () => {
    it('offers a stop while the work is in flight, and calls it once', () => {
        const onCancel = vi.fn();
        render({ transfer: transfer(), onCancel });
        const button = host.querySelector<HTMLButtonElement>('button[aria-label="Stop plan.pdf"]');
        button?.click();
        expect(onCancel).toHaveBeenCalledOnce();
    });

    it('offers nothing to stop where the caller says it cannot be stopped', () => {
        render({ transfer: transfer({ status: 'queued' }) });
        expect(host.querySelector('button')).toBeNull();
    });

    it('offers a retry to a failed download, and nothing to a finished one', () => {
        const onRetry = vi.fn();
        render({ transfer: transfer({ status: 'failed' }), onRetry });
        const button = host.querySelector<HTMLButtonElement>('button[aria-label="Retry plan.pdf"]');
        button?.click();
        expect(onRetry).toHaveBeenCalledOnce();
    });

    it('offers a share to a finished download that kept its file', () => {
        const onShare = vi.fn();
        render({ transfer: transfer({ status: 'done', progress: 100 }), onShare });
        host.querySelector<HTMLButtonElement>('button[aria-label="Share plan.pdf"]')?.click();
        expect(onShare).toHaveBeenCalledOnce();
    });

    it('prefers stopping live work over an offer about work already done', () => {
        const onCancel = vi.fn();
        const onShare = vi.fn();
        render({ transfer: transfer(), onCancel, onShare });
        expect(host.querySelectorAll('button')).toHaveLength(1);
        expect(host.querySelector('button')?.getAttribute('aria-label')).toBe('Stop plan.pdf');
    });
});

describe('where a phone download went', () => {
    it('keeps the answer on the row, where it can be asked for again', () => {
        render({
            transfer: transfer({
                status: 'done',
                progress: 100,
                finishedAt: Date.now(),
                note: 'Saved to Files › TDrive › Downloads',
            }),
        });
        expect(host.textContent).toContain('Saved to Files › TDrive › Downloads');
    });

    it('says nothing extra when there is nothing to add', () => {
        render({ transfer: transfer({ status: 'done', progress: 100 }) });
        expect(host.querySelector('.note')).toBeNull();
    });
});

describe('the files under an aggregate row', () => {
    const batch = () => transfer({
        id: 'xfer:up:upload-batch',
        direction: 'up',
        name: 'Uploading 200 files',
        progress: 18,
        total: 0,
        bytes: 5 * 1024 * 1024,
        itemsDone: 36,
        itemsTotal: 200,
        itemsActive: 6,
        items: [
            { key: '36', name: 'clip.mov', progress: 40, total: 1024 * 1024 * 4 },
            { key: '37', name: 'notes.txt', progress: 10, total: 0 },
        ],
    });

    it('says how the batch is doing and which files are actually moving', () => {
        render({ transfer: batch() });
        // The row: how many files, how much has gone, how long is left.
        expect(host.textContent).toContain('36 of 200 files');
        expect(host.textContent).toContain('5 MB so far');
        // The files under it, each with its own figure and bar.
        expect(host.textContent).toContain('clip.mov');
        expect(host.textContent).toContain('1.6 of 4 MB');
        expect(host.textContent).toContain('10%');
        // Four more are uploading than the list has room to name.
        expect(host.textContent).toContain('+4 more files');
        expect(host.querySelectorAll('[role="progressbar"]')).toHaveLength(3);
    });

    it('drops the list when the batch finishes, so a done row lists nothing', () => {
        render({ transfer: { ...batch(), status: 'done', items: undefined, itemsActive: 0 } });
        expect(host.textContent).not.toContain('clip.mov');
    });

    it('offers a thumb-sized stop on each file, keyed by the file it stops', async () => {
        const stopped: string[] = [];
        render({ transfer: batch(), onCancelFile: (key: string) => stopped.push(key) });
        const stop = host.querySelector<HTMLButtonElement>('button[aria-label="Stop clip.mov"]');
        expect(stop).not.toBeNull();
        stop?.click();
        flushSync();
        expect(stopped).toEqual(['36']);
    });
});
