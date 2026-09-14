import { afterAll, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import ToastStack from '../ui/notifications/ToastStack.svelte';
import { get } from 'svelte/store';
import {
    activateNotificationEffects,
    clearAllNotifications,
    dismissNotification,
    notify,
    pauseAllNotifications,
    pauseToast,
    resumeAllNotifications,
    resumeToast,
} from './notifications';
import { toasts } from '../ui/notifications/toast-store';

const START_TIME = new Date('2026-01-01T00:00:00.000Z');

let host: HTMLElement;
let app: Record<string, unknown> | null = null;
let disposeNotificationEffects: (() => void) | undefined;

function toast(id: string): HTMLElement | null {
    return document.querySelector<HTMLElement>(`.toast[data-id="${id}"]`);
}

beforeAll(() => {
    vi.useFakeTimers();
    vi.setSystemTime(START_TIME);
    host = document.createElement('div');
    host.id = 'toast-stack';
    host.setAttribute('role', 'status');
    host.setAttribute('aria-live', 'polite');
    document.body.appendChild(host);
    app = mount(ToastStack, {
        target: host,
        props: {
            onDismiss: dismissNotification,
            onPauseToast: pauseToast,
            onResumeToast: resumeToast,
            onPauseAll: pauseAllNotifications,
            onResumeAll: resumeAllNotifications,
        },
    });
    disposeNotificationEffects = activateNotificationEffects();
    flushSync();
});

beforeEach(() => {
    clearAllNotifications();
    vi.setSystemTime(START_TIME);
    flushSync();
});

afterAll(async () => {
    disposeNotificationEffects?.();
    disposeNotificationEffects = undefined;
    clearAllNotifications();
    if (app) await unmount(app);
    app = null;
    host.remove();
    vi.useRealTimers();
});

describe('notification expiry scheduling', () => {
    it('expires at the nearest deadline without running an animation-frame loop', () => {
        const animationFrame = vi.spyOn(window, 'requestAnimationFrame');

        notify({ id: 'first', title: 'First', durationMs: 1_000 });
        notify({ id: 'second', title: 'Second', durationMs: 3_000 });
        flushSync();

        expect(animationFrame).not.toHaveBeenCalled();
        expect(document.getElementById('toast-stack')).toMatchObject({
            role: 'status',
        });
        expect(document.getElementById('toast-stack')?.getAttribute('aria-live')).toBe('polite');
        expect(toast('first')?.getAttribute('role')).toBe('status');

        vi.advanceTimersByTime(999);
        flushSync();
        expect(toast('first')).not.toBeNull();
        expect(toast('second')).not.toBeNull();

        vi.advanceTimersByTime(1);
        flushSync();
        expect(toast('first')).toBeNull();
        expect(toast('second')).not.toBeNull();

        vi.advanceTimersByTime(2_000);
        flushSync();
        expect(toast('second')).toBeNull();
    });

    it('reschedules when the nearest toast is dismissed or replaced', () => {
        notify({ id: 'later', title: 'Later', durationMs: 5_000 });
        notify({ id: 'soon', title: 'Soon', durationMs: 1_000 });
        vi.advanceTimersByTime(200);
        dismissNotification('soon');

        notify({ id: 'changing', title: 'Changing', durationMs: 3_000 });
        vi.advanceTimersByTime(200);
        notify({ id: 'changing', title: 'Changed', durationMs: 400 });
        flushSync();

        vi.advanceTimersByTime(399);
        flushSync();
        expect(toast('changing')?.textContent).toContain('Changed');

        vi.advanceTimersByTime(1);
        flushSync();
        expect(toast('changing')).toBeNull();
        expect(toast('later')).not.toBeNull();

        vi.advanceTimersByTime(4_200);
        flushSync();
        expect(toast('later')).toBeNull();
    });

    it('freezes the exact remaining time while a toast is hovered', () => {
        notify({ id: 'paused', title: 'Paused', durationMs: 1_000 });
        flushSync();
        vi.advanceTimersByTime(400);

        toast('paused')?.dispatchEvent(new MouseEvent('mouseenter'));
        flushSync();
        expect(get(toasts)[0]).toMatchObject({ paused: true, remainingMs: 600 });

        vi.advanceTimersByTime(10_000);
        flushSync();
        expect(toast('paused')).not.toBeNull();

        toast('paused')?.dispatchEvent(new MouseEvent('mouseleave'));
        vi.advanceTimersByTime(599);
        flushSync();
        expect(toast('paused')).not.toBeNull();

        vi.advanceTimersByTime(1);
        flushSync();
        expect(toast('paused')).toBeNull();
    });

    it('keeps every countdown frozen while the stack remains hovered', () => {
        notify({ id: 'short', title: 'Short', durationMs: 1_000 });
        notify({ id: 'long', title: 'Long', durationMs: 2_000 });
        flushSync();
        vi.advanceTimersByTime(400);

        const stack = document.querySelector<HTMLElement>('.toast-stack-inner');
        stack?.dispatchEvent(new MouseEvent('mouseenter'));
        toast('short')?.dispatchEvent(new MouseEvent('mouseenter'));
        toast('short')?.dispatchEvent(new MouseEvent('mouseleave'));
        vi.advanceTimersByTime(10_000);
        flushSync();

        expect(toast('short')).not.toBeNull();
        expect(toast('long')).not.toBeNull();

        stack?.dispatchEvent(new MouseEvent('mouseleave'));
        vi.advanceTimersByTime(600);
        flushSync();
        expect(toast('short')).toBeNull();
        expect(toast('long')).not.toBeNull();

        vi.advanceTimersByTime(1_000);
        flushSync();
        expect(toast('long')).toBeNull();
    });

    it('reconciles overdue deadlines immediately when the app wakes', () => {
        notify({ id: 'asleep', title: 'Asleep', durationMs: 1_000 });
        flushSync();

        vi.setSystemTime(new Date(START_TIME.getTime() + 5_000));
        window.dispatchEvent(new Event('focus'));
        flushSync();

        expect(toast('asleep')).toBeNull();
    });
});
