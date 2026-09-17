import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';

vi.mock('../../api', async (importOriginal) => {
    const actual = await importOriginal<typeof import('../../api')>();
    return { ...actual, isMobilePlatform: () => true };
});

import ToastStack from './ToastStack.svelte';
import { toasts } from './toast-store';

let host: HTMLElement;
let app: Record<string, unknown> | null = null;

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

describe('mobile error toasts', () => {
    it('keeps a compact detail line visible and leaves touch actions alone', () => {
        const dismiss = vi.fn();
        const retry = vi.fn();
        toasts.set([{
            id: 'failed-upload',
            level: 'error',
            title: 'Upload failed',
            body: 'The connection was interrupted. Try again when you are online.',
            action: { label: 'Retry', run: retry },
            sticky: true,
            spinner: false,
            durationMs: 0,
            expiresAt: 0,
            paused: false,
            remainingMs: 0,
        }]);
        app = mount(ToastStack, {
            target: host,
            props: {
                onDismiss: dismiss,
                onPauseToast: vi.fn(),
                onResumeToast: vi.fn(),
                onPauseAll: vi.fn(),
                onResumeAll: vi.fn(),
            },
        });
        flushSync();

        const detail = host.querySelector<HTMLElement>('.toast-error .toast-body');
        const toast = host.querySelector<HTMLElement>('.toast-error');
        const retryButton = host.querySelector<HTMLButtonElement>('.toast-action');
        const closeButton = host.querySelector<HTMLButtonElement>('.toast-close');
        expect(detail?.textContent).toContain('connection was interrupted');
        expect(toast?.classList.contains('has-mobile-detail')).toBe(true);
        expect(detail?.id).toBe('toast-detail-failed-upload');
        expect(toast?.getAttribute('aria-describedby')).toBe('toast-detail-failed-upload');
        // The visual glyphs stay compact, but the actual controls carry a
        // platform-safe touch target rather than asking a finger to hit text or
        // a 14px close icon.
        expect(retryButton?.classList.contains('toast-interactive-control')).toBe(true);
        expect(closeButton?.classList.contains('toast-interactive-control')).toBe(true);

        retryButton?.dispatchEvent(new PointerEvent('pointerup', { pointerType: 'touch', bubbles: true }));
        retryButton?.click();
        expect(retry).toHaveBeenCalledOnce();
        expect(dismiss).not.toHaveBeenCalled();
    });
});
