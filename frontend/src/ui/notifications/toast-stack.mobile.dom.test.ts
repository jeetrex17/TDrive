import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';

vi.mock('../../api', async (importOriginal) => {
    const actual = await importOriginal<typeof import('../../api')>();
    return { ...actual, isMobilePlatform: () => true };
});

import ToastStack from './ToastStack.svelte';
import { toasts, type ToastItem } from './toast-store';
import { DISMISS_EXIT_MS } from './swipe-dismiss';

let host: HTMLElement;
let app: Record<string, unknown> | null = null;

function toast(overrides: Partial<ToastItem> = {}): ToastItem {
    return {
        id: 'failed-upload',
        level: 'error',
        title: 'Upload failed',
        body: 'The connection was interrupted. Try again when you are online.',
        sticky: false,
        spinner: false,
        durationMs: 8000,
        expiresAt: Date.now() + 8000,
        paused: false,
        ...overrides,
    };
}

function render(onDismiss = vi.fn()) {
    app = mount(ToastStack, {
        target: host,
        props: {
            onDismiss,
            onPauseToast: vi.fn(),
            onResumeToast: vi.fn(),
            onPauseAll: vi.fn(),
            onResumeAll: vi.fn(),
        },
    });
    flushSync();
    return onDismiss;
}

/** A touch drag the stack can read: pointerdown, a move, then a release. */
function swipe(node: Element, distance: number): void {
    node.dispatchEvent(new PointerEvent('pointerdown', { pointerType: 'touch', clientY: 0, bubbles: true }));
    node.dispatchEvent(new PointerEvent('pointermove', { pointerType: 'touch', clientY: distance, bubbles: true }));
    node.dispatchEvent(new PointerEvent('pointerup', { pointerType: 'touch', clientY: distance, bubbles: true }));
}

beforeEach(() => {
    document.documentElement.classList.add('mobile');
    host = document.createElement('div');
    document.body.append(host);
});

afterEach(() => {
    toasts.set([]);
    if (app) unmount(app);
    app = null;
    host.remove();
    document.documentElement.classList.remove('mobile');
});

describe('mobile toasts', () => {
    it('shows the detail line and keeps touch actions to themselves', () => {
        const retry = vi.fn();
        toasts.set([toast({ action: { label: 'Retry', run: retry } })]);
        const dismiss = render();

        const detail = host.querySelector<HTMLElement>('.toast-error .toast-body');
        const card = host.querySelector<HTMLElement>('.toast-error');
        const retryButton = host.querySelector<HTMLButtonElement>('.toast-action');
        const closeButton = host.querySelector<HTMLButtonElement>('.toast-close');

        // The body is the half of a failure that says what to do about it, so
        // it is rendered on a phone as it is everywhere else.
        expect(detail?.textContent).toContain('connection was interrupted');
        expect(detail?.id).toBe('toast-detail-failed-upload');
        expect(card?.getAttribute('aria-describedby')).toBe('toast-detail-failed-upload');

        // The visual glyphs stay compact, but the actual controls carry a
        // platform-safe touch target rather than asking a finger to hit text or
        // a 14px close icon.
        expect(retryButton?.classList.contains('toast-interactive-control')).toBe(true);
        expect(closeButton?.classList.contains('toast-interactive-control')).toBe(true);

        retryButton?.dispatchEvent(new PointerEvent('pointerdown', { pointerType: 'touch', bubbles: true }));
        retryButton?.dispatchEvent(new PointerEvent('pointerup', { pointerType: 'touch', bubbles: true }));
        retryButton?.click();
        expect(retry).toHaveBeenCalledOnce();
        expect(dismiss).not.toHaveBeenCalled();
    });

    it('dismisses on a tap and on a swipe down, and holds on a short drag', () => {
        toasts.set([toast()]);
        const dismiss = render();
        const card = host.querySelector<HTMLElement>('.toast')!;

        // A press that never moved is a tap: the notice has been read.
        swipe(card, 0);
        expect(dismiss).toHaveBeenCalledOnce();

        // Past the tap slop but short of the threshold: a drag that was not
        // going anywhere, so the toast springs back rather than leaving.
        dismiss.mockClear();
        swipe(card, 20);
        expect(dismiss).not.toHaveBeenCalled();

        // Far enough down is a dismissal, and the surface carries on off the
        // bottom edge before handing over rather than blinking out under the
        // finger that threw it.
        vi.useFakeTimers();
        try {
            swipe(card, 400);
            expect(dismiss).not.toHaveBeenCalled();
            vi.advanceTimersByTime(DISMISS_EXIT_MS);
            expect(dismiss).toHaveBeenCalledOnce();
        } finally {
            vi.useRealTimers();
        }
    });

    it('leaves a mouse alone, so a drag over a toast can still select its text', () => {
        toasts.set([toast()]);
        const dismiss = render();
        const card = host.querySelector<HTMLElement>('.toast')!;

        card.dispatchEvent(new PointerEvent('pointerdown', { pointerType: 'mouse', clientY: 0, bubbles: true }));
        card.dispatchEvent(new PointerEvent('pointerup', { pointerType: 'mouse', clientY: 0, bubbles: true }));
        expect(dismiss).not.toHaveBeenCalled();
    });
});
