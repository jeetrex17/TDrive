// Behavior tests for notification history, hover disclosure, auto-close, and
// terminal transfer invariants.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import NotifBell from '../ui/notifications/NotifBell.svelte';
import { get } from 'svelte/store';
import {
    cancelTransfersInDirection,
    clearHistory,
    markTransferDone,
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


let host: HTMLElement;
let app: Record<string, unknown> | null = null;

function bell(): HTMLElement {
    const el = document.getElementById('notif-bell');
    if (!el) throw new Error('bell not rendered');
    return el;
}

function reset(): void {
    notifPanelOpen.set(false);
    notifUnreadErrors.set(0);
    historyEvents.set([]);
    flushSync();
}

beforeEach(() => {
    host = document.createElement('div');
    host.id = 'notif-bell-root';
    document.body.appendChild(host);
    app = mount(NotifBell, {
        target: host,
        props: {
            onCancelDirection: cancelTransfersInDirection,
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
        expect(document.body.textContent).toContain('3 / 5 files');
    });

    it('opens the full panel on hover and closes after the pointer leaves', () => {
        vi.useFakeTimers();
        try {
            pushTransferStart({ id: 3, direction: 'up', name: 'c.bin', total: 10 });
            pushHistoryEvent({ level: 'error', title: 'Could not join drive', body: 'expired' });
            flushSync();
            expect(get(notifUnreadErrors)).toBe(1);

            bell().dispatchEvent(new MouseEvent('mouseenter'));
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
            vi.runAllTimers();
            flushSync();
            expect(get(notifPanelOpen)).toBe(true);

            panel?.dispatchEvent(new MouseEvent('mouseleave'));
            vi.runAllTimers();
            flushSync();
            expect(get(notifPanelOpen)).toBe(false);
        } finally {
            vi.useRealTimers();
        }
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
