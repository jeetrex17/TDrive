// Browser behavior for auth forms and value retention.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import AuthScreens from './AuthScreens.svelte';
import { showAuthView, showStartupView } from '../app/app-store';
import {
    authPhone,
    beginAuthSubmission,
    failAuthSubmission,
    resetAuthSubmissions,
} from './auth-store';

let host: HTMLElement;
let app: Record<string, unknown>;
let onPhone: ReturnType<typeof vi.fn<(phone: string) => void>>;
let onCode: ReturnType<typeof vi.fn<(code: string) => void>>;

function codeInput(): HTMLInputElement {
    const el = host.querySelector('.code-input') as HTMLInputElement | null;
    if (!el) throw new Error('code input not rendered');
    return el;
}

beforeEach(() => {
    showStartupView();
    authPhone.set('');
    resetAuthSubmissions();
    onPhone = vi.fn();
    onCode = vi.fn(() => { void beginAuthSubmission('code'); });
    host = document.createElement('div');
    document.body.appendChild(host);
    app = mount(AuthScreens, {
        target: host,
        props: {
            onSetup: vi.fn(),
            onPhone,
            onCode,
            onPassword: vi.fn(),
            onBackToPhone: vi.fn(),
        },
    });
});

afterEach(async () => {
    showStartupView();
    resetAuthSubmissions();
    flushSync();
    await unmount(app);
    host.remove();
});

describe('AuthScreens interactions', () => {
    it('clears the code field when transitioning onto the code screen', () => {
        showAuthView('code');
        flushSync();
        const input = codeInput();
        input.value = '123';
        input.dispatchEvent(new Event('input', { bubbles: true }));
        flushSync();

        // Leave and return: the transition back clears the field.
        showAuthView('phone');
        flushSync();
        showAuthView('code');
        flushSync();

        expect(codeInput().value).toBe('');
    });

    it('keeps the entered code when Telegram rejects it and announces the error inline', () => {
        showAuthView('code');
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
        showAuthView('code');
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


describe('phone entry', () => {
    function enterPhone(value: string): void {
        const input = host.querySelector<HTMLInputElement>('#telegram-phone')!;
        input.value = value;
        input.dispatchEvent(new Event('input', { bubbles: true }));
        flushSync();
    }
    function send(): void {
        host.querySelector('form')!.dispatchEvent(new SubmitEvent('submit', { bubbles: true, cancelable: true }));
        flushSync();
    }
    it('normalizes a full pasted number and retains it when returning from code', () => {
        showAuthView('phone');
        flushSync();
        enterPhone('+91 98765 43210');
        send();
        expect(onPhone).toHaveBeenCalledWith('+919876543210');
        showAuthView('code');
        flushSync();
        showAuthView('phone');
        flushSync();
        expect(host.querySelector<HTMLInputElement>('#telegram-phone')!.value).toBe('+91 98765 43210');
        expect(host.querySelector('#telegram-country')?.textContent).toContain('+91');
    });
    it('does not send an ambiguous national number without a country', () => {
        showAuthView('phone');
        flushSync();
        enterPhone('9876543210');
        send();
        expect(onPhone).not.toHaveBeenCalled();
        expect(host.querySelector('#phone-error')?.textContent).toContain('Choose a country');
    });
});
