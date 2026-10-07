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

/**
 * Hovering is a pointer question now, not a mouse question: a finger reports
 * pointerType "touch" and the stack has to ignore it, because a touch host
 * sends mouseenter on a tap and never sends the matching mouseleave.
 */
function pointer(type: string, pointerType: 'mouse' | 'touch'): Event {
    const event = new MouseEvent(type, { bubbles: false }) as MouseEvent & { pointerType?: string };
    Object.defineProperty(event, 'pointerType', { value: pointerType });
    return event;
}

describe('notification expiry scheduling', () => {
    it.each([
        ['info', 4_000],
        ['success', 4_000],
        ['warning', 6_000],
        ['error', 8_000],
    ] as const)('dismisses a %s notice after %i ms', (level, duration) => {
        notify({ id: 'level-default', level, title: 'Status' });
        expect(get(toasts)[0]?.durationMs).toBe(duration);
        vi.advanceTimersByTime(duration - 1);
        expect(get(toasts)).toHaveLength(1);
        vi.advanceTimersByTime(1);
        expect(get(toasts)).toHaveLength(0);
    });

    it('bounds old sticky and zero-duration requests, including progress spinners', () => {
        notify({ id: 'legacy-sticky', title: 'Working', sticky: true, spinner: true });
        notify({ id: 'legacy-zero', level: 'warning', title: 'Offline', durationMs: 0 });
        expect(get(toasts).map(({ sticky, durationMs }) => ({ sticky, durationMs }))).toEqual([
            { sticky: false, durationMs: 4_000 },
            { sticky: false, durationMs: 6_000 },
        ]);
        vi.advanceTimersByTime(6_000);
        expect(get(toasts)).toHaveLength(0);
    });

    it('allows at least eight seconds for a toast action', () => {
        notify({ id: 'undo', title: 'Moved to Trash', durationMs: 1_000, action: { label: 'Undo', run: vi.fn() } });
        expect(get(toasts)[0]?.durationMs).toBe(8_000);
        vi.advanceTimersByTime(7_999);
        expect(get(toasts)).toHaveLength(1);
        vi.advanceTimersByTime(1);
        expect(get(toasts)).toHaveLength(0);
    });

    it('shows the final result even after an in-progress notice expires', () => {
        notify({ id: 'creating', title: 'Creating drive', spinner: true });
        vi.advanceTimersByTime(4_000);
        expect(get(toasts)).toHaveLength(0);

        notify({ id: 'creating', level: 'success', title: 'Drive created' });
        expect(get(toasts)[0]).toMatchObject({ title: 'Drive created', spinner: false });
        vi.advanceTimersByTime(4_000);
        expect(get(toasts)).toHaveLength(0);
    });

    it('keeps no more than three desktop notices visible', () => {
        for (const id of ['one', 'two', 'three', 'four']) notify({ id, title: id });
        expect(get(toasts).map(({ id }) => id)).toEqual(['two', 'three', 'four']);
    });

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

    it('does not let a finger freeze a toast on screen', () => {
        // A touch host fires mouseenter on a tap and withholds mouseleave until
        // the next tap elsewhere, so pausing on it left phone toasts up for
        // good -- and the stack sits right above the tab bar, where every tap
        // lands. Verified on device before this was written.
        notify({ id: 'tapped', title: 'Tapped', durationMs: 1_000 });
        flushSync();

        toast('tapped')?.dispatchEvent(pointer('pointerenter', 'touch'));
        flushSync();
        expect(get(toasts)[0]).toMatchObject({ paused: false });

        vi.advanceTimersByTime(1_001);
        flushSync();
        expect(toast('tapped')).toBeNull();
    });

    it('still freezes for a pointer that can really hover', () => {
        notify({ id: 'hovered', title: 'Hovered', durationMs: 1_000 });
        flushSync();

        toast('hovered')?.dispatchEvent(pointer('pointerenter', 'mouse'));
        vi.advanceTimersByTime(5_000);
        flushSync();
        expect(toast('hovered')).not.toBeNull();
    });

    it('freezes the exact remaining time while a toast is hovered', () => {
        notify({ id: 'paused', title: 'Paused', durationMs: 1_000 });
        flushSync();
        vi.advanceTimersByTime(400);

        toast('paused')?.dispatchEvent(pointer('pointerenter', 'mouse'));
        flushSync();
        expect(get(toasts)[0]).toMatchObject({ paused: true, remainingMs: 600 });

        vi.advanceTimersByTime(10_000);
        flushSync();
        expect(toast('paused')).not.toBeNull();

        toast('paused')?.dispatchEvent(pointer('pointerleave', 'mouse'));
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
        stack?.dispatchEvent(pointer('pointerenter', 'mouse'));
        toast('short')?.dispatchEvent(pointer('pointerenter', 'mouse'));
        toast('short')?.dispatchEvent(pointer('pointerleave', 'mouse'));
        vi.advanceTimersByTime(10_000);
        flushSync();

        expect(toast('short')).not.toBeNull();
        expect(toast('long')).not.toBeNull();

        stack?.dispatchEvent(pointer('pointerleave', 'mouse'));
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

    it('finishes a toast once when it expires or is dismissed', () => {
        const expired = vi.fn();
        const dismissed = vi.fn();
        const replaced = vi.fn();
        const cleared = vi.fn();
        notify({ id: 'expired', title: 'Expires', durationMs: 1_000, onRemoved: expired });
        notify({ id: 'dismissed', title: 'Dismissed', onRemoved: dismissed });
        notify({ id: 'replaced', title: 'Before', onRemoved: replaced });
        notify({ id: 'replaced', title: 'After' });
        notify({ id: 'cleared', title: 'Cleared', onRemoved: cleared });

        dismissNotification('dismissed');
        dismissNotification('dismissed');
        expect(dismissed).toHaveBeenCalledOnce();
        expect(replaced).toHaveBeenCalledOnce();

        vi.advanceTimersByTime(1_000);
        expect(expired).toHaveBeenCalledOnce();
        clearAllNotifications();
        expect(cleared).toHaveBeenCalledOnce();
    });
});
