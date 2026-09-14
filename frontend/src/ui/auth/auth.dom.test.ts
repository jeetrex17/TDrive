// Browser behavior for auth forms and value retention.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import AuthScreens from './AuthScreens.svelte';
import {
    authPhone,
    authScreen,
    beginAuthSubmission,
    failAuthSubmission,
    resetAuthSubmissions,
} from './auth-store';

let host: HTMLElement;
let app: Record<string, unknown>;
let onCode: ReturnType<typeof vi.fn<(code: string) => void>>;

function codeInput(): HTMLInputElement {
    const el = host.querySelector('.code-input') as HTMLInputElement | null;
    if (!el) throw new Error('code input not rendered');
    return el;
}

beforeEach(() => {
    authScreen.set(null);
    authPhone.set('');
    resetAuthSubmissions();
    onCode = vi.fn(() => { void beginAuthSubmission('code'); });
    host = document.createElement('div');
    document.body.appendChild(host);
    app = mount(AuthScreens, {
        target: host,
        props: {
            onSetup: vi.fn(),
            onPhone: vi.fn(),
            onCode,
            onPassword: vi.fn(),
            onBackToPhone: vi.fn(),
        },
    });
});

afterEach(async () => {
    authScreen.set(null);
    resetAuthSubmissions();
    flushSync();
    await unmount(app);
    host.remove();
});

describe('AuthScreens interactions', () => {
    it('clears the code field when transitioning onto the code screen', () => {
        authScreen.set('code');
        flushSync();
        const input = codeInput();
        input.value = '123';
        input.dispatchEvent(new Event('input', { bubbles: true }));
        flushSync();

        // Leave and return: the transition back clears the field.
        authScreen.set('phone');
        flushSync();
        authScreen.set('code');
        flushSync();

        expect(codeInput().value).toBe('');
    });

    it('keeps the entered code when Telegram rejects it and announces the error inline', () => {
        authScreen.set('code');
        flushSync();
        const input = codeInput();
        input.value = '999';
        input.dispatchEvent(new Event('input', { bubbles: true }));
        beginAuthSubmission('code');
        failAuthSubmission('code', 'That code was incorrect. Try again.');
        flushSync();

        expect(codeInput().value).toBe('999');
        expect(codeInput().getAttribute('aria-invalid')).toBe('true');
        expect(host.querySelector('#code-error')?.textContent).toContain('That code was incorrect.');
    });

    it('blocks a duplicate form submission and disables the active flow controls', () => {
        authScreen.set('code');
        flushSync();
        const form = host.querySelector<HTMLFormElement>('form');
        if (!form) throw new Error('code form not rendered');

        form.dispatchEvent(new SubmitEvent('submit', { bubbles: true, cancelable: true }));
        form.dispatchEvent(new SubmitEvent('submit', { bubbles: true, cancelable: true }));
        flushSync();

        expect(onCode).toHaveBeenCalledOnce();
        expect(codeInput().disabled).toBe(true);
        expect(host.querySelector<HTMLButtonElement>('button[type="submit"]')?.disabled).toBe(true);
        expect(host.querySelector<HTMLButtonElement>('button[type="submit"]')?.textContent).toContain('Verifying');
        expect(form.getAttribute('aria-busy')).toBe('true');
    });
});
