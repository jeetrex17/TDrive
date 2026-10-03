// The bell's popover is the only place a desktop reader can stop a transfer or
// copy an error, and it lives in a document portal outside the header. These
// cover the paths that made it unreachable: focus never entering it, Tab
// walking straight back out into the header, and an activation that only ever
// opened.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, tick, unmount } from 'svelte';
import { get } from 'svelte/store';
import NotifBell from './NotifBell.svelte';
import { historyEvents, notifPanelOpen, notifUnreadErrors } from './notif-store';
import { resumableUploads } from '../../modules/resumable-uploads';

const HOVER_INTENT_MS = 140;

let host: HTMLElement;
let app: Record<string, unknown> | null = null;

function bell(): HTMLButtonElement {
    const el = document.querySelector<HTMLButtonElement>('#notif-bell');
    if (!el) throw new Error('Expected the notification bell');
    return el;
}

function panel(): HTMLElement | null {
    return document.querySelector<HTMLElement>('.notif-panel');
}

/** Lets the open path's `await tick()` land before focus is inspected. */
async function settle(): Promise<void> {
    flushSync();
    await tick();
    await tick();
}

function press(target: EventTarget, key: string, init: KeyboardEventInit = {}): void {
    target.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true, ...init }));
}

beforeEach(() => {
    historyEvents.set([
        { kind: 'event', id: 'err-1', level: 'error', title: 'Could not join drive', body: 'INVITE_HASH_EXPIRED', ts: Date.now() },
    ]);
    notifUnreadErrors.set(1);
    resumableUploads.set([]);
    host = document.createElement('div');
    document.body.appendChild(host);
    app = mount(NotifBell, {
        target: host,
        props: { onCancelDirection: vi.fn(), onCancelFile: vi.fn(), onClearHistory: vi.fn() },
    });
    flushSync();
});

afterEach(async () => {
    notifPanelOpen.set(false);
    notifUnreadErrors.set(0);
    historyEvents.set([]);
    resumableUploads.set([]);
    flushSync();
    if (app) await unmount(app);
    app = null;
    host.remove();
});

describe('notification panel keyboard reachability', () => {
    it('moves focus into the panel when it is opened from the keyboard', async () => {
        bell().focus();
        press(bell(), 'Enter');
        await settle();

        const open = panel();
        expect(open).not.toBeNull();
        expect(document.activeElement).toBe(open);
    });

    it('keeps Tab inside the panel instead of letting it walk into the header', async () => {
        bell().focus();
        press(bell(), 'Enter');
        await settle();

        const open = panel();
        if (!open) throw new Error('Expected the panel');
        const stops = Array.from(open.querySelectorAll<HTMLElement>('button:not([disabled])'));
        // Clear plus the error row's Copy details: enough to have two edges.
        expect(stops.length).toBeGreaterThan(1);

        // From the container, the first Tab steps in rather than out.
        press(open, 'Tab');
        expect(document.activeElement).toBe(stops[0]);

        // And the last stop wraps to the first rather than reaching the header.
        stops[stops.length - 1].focus();
        press(open, 'Tab');
        expect(document.activeElement).toBe(stops[0]);

        // Backwards from the first wraps to the last, not back onto the bell.
        press(open, 'Tab', { shiftKey: true });
        expect(document.activeElement).toBe(stops[stops.length - 1]);
    });

    it('toggles on activation and hands focus back to the bell', async () => {
        bell().focus();
        press(bell(), 'Enter');
        await settle();
        expect(bell().getAttribute('aria-expanded')).toBe('true');

        press(bell(), 'Enter');
        await settle();
        expect(panel()).toBeNull();
        expect(bell().getAttribute('aria-expanded')).toBe('false');
        expect(document.activeElement).toBe(bell());

        bell().click();
        await settle();
        expect(panel()).not.toBeNull();
        bell().click();
        await settle();
        expect(panel()).toBeNull();
    });

    it('says what it is about rather than only colouring itself', () => {
        expect(bell().getAttribute('aria-label')).toBe('Notifications, 1 error');

        notifUnreadErrors.set(0);
        historyEvents.set([
            {
                kind: 'transfer',
                id: 'xfer:up:1',
                direction: 'up',
                name: 'a.bin',
                progress: 10,
                total: 100,
                bytes: 10,
                speed: 0,
                status: 'active',
                startedAt: 0,
                finishedAt: 0,
            },
        ]);
        flushSync();
        expect(bell().getAttribute('aria-label')).toBe('Notifications, 1 transfer in progress');

        historyEvents.set([]);
        flushSync();
        expect(bell().getAttribute('aria-label')).toBe('Notifications');
    });

    it('shows backend-confirmed bytes and offers distinct resume and discard actions', async () => {
        notifUnreadErrors.set(0);
        historyEvents.set([{
            kind: 'transfer', id: 'xfer:up:3', direction: 'up', name: 'archive.bin',
            progress: 60, bytes: 600, total: 1_000, speed: 0,
            status: 'failed', startedAt: 0, finishedAt: 1,
            resumableJobId: 'job-7',
        }]);
        resumableUploads.set([{
            jobId: 'job-7', channelId: 1, name: 'archive.bin', size: 1_000,
            confirmedBytes: 400, status: 'paused', error: '',
        }]);
        bell().click();
        await settle();

        const open = panel();
        expect(open?.querySelectorAll('.resumable-row')).toHaveLength(1);
        expect(open?.textContent).toContain('confirmed');
        expect(open?.querySelector('[aria-valuenow="40"]')).not.toBeNull();
        expect(open?.textContent).not.toContain('Failed');
        expect(Array.from(open?.querySelectorAll('button') ?? []).some((button) => button.textContent === 'Resume')).toBe(true);
        const discard = Array.from(open?.querySelectorAll('button') ?? []).find((button) => button.textContent === 'Discard');
        discard?.click();
        await settle();
        expect(open?.textContent).toContain('Discard parts');
        expect(open?.textContent).toContain('Keep');

        resumableUploads.set([{
            jobId: 'job-7', channelId: 1, name: 'archive.bin', size: 1_000,
            confirmedBytes: 400, status: 'waiting_network', error: '',
        }]);
        await settle();
        expect(open?.textContent).toContain('Waiting for connection');
        expect(open?.textContent).toContain('retry when');
        expect(open?.querySelector<HTMLButtonElement>('button[aria-label="Retry archive.bin"]')?.textContent).toBe('Retry now');
        expect(open?.textContent).toContain('Discard');

        resumableUploads.set([{
            jobId: 'job-7', channelId: 1, name: 'archive.bin', size: 1_000,
            confirmedBytes: 400, status: 'uncertain_manifest', error: '',
        }]);
        await settle();
        expect(open?.textContent).toContain('Check and retry');
        expect(open?.textContent).not.toContain('Discard parts');
        expect(open?.textContent).not.toContain('Resume');

        resumableUploads.set([{
            jobId: 'job-7', channelId: 1, name: 'archive.bin', size: 1_000,
            confirmedBytes: 400, status: 'restart_required', error: '',
        }]);
        await settle();
        expect(open?.textContent).toContain('start a new upload');
        expect(open?.textContent).not.toContain('Resume');
        expect(open?.textContent).not.toContain('Choose file');
    });
});

describe('hover intent', () => {
    beforeEach(() => vi.useFakeTimers());
    afterEach(() => vi.useRealTimers());

    it('opens nothing for a pointer that only crosses the bell', () => {
        bell().dispatchEvent(new MouseEvent('mouseenter'));
        vi.advanceTimersByTime(HOVER_INTENT_MS - 1);
        flushSync();
        expect(get(notifPanelOpen)).toBe(false);

        bell().dispatchEvent(new MouseEvent('mouseleave'));
        vi.advanceTimersByTime(HOVER_INTENT_MS * 4);
        flushSync();
        expect(get(notifPanelOpen)).toBe(false);
    });

    it('opens once the pointer has stayed for the intent delay', () => {
        bell().dispatchEvent(new MouseEvent('mouseenter'));
        vi.advanceTimersByTime(HOVER_INTENT_MS);
        flushSync();
        expect(get(notifPanelOpen)).toBe(true);
        // Hover must not pull focus away from whatever the reader is using.
        expect(panel()).not.toBe(document.activeElement);
    });
});
