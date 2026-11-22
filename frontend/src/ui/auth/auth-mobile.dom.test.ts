// Phone-only behaviour: the welcome page, return-key hand-off, and tap guards.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, tick, unmount } from 'svelte';
import { welcomeSeen } from './welcome-store';
import AuthScreens from './AuthScreens.svelte';
import { showAuthView, showStartupView } from '../app/app-store';
import { beginAuthSubmission, resetAuthSubmissions } from './auth-store';
import { closeTopSheet, hasOpenSheet } from '../modals/sheet-stack';

let host: HTMLElement;
let app: Record<string, unknown>;
let onBackToPhone: ReturnType<typeof vi.fn<() => void>>;

function field(id: string): HTMLInputElement {
    const el = host.querySelector<HTMLInputElement>(`#${id}`);
    if (!el) throw new Error(`${id} not rendered`);
    return el;
}

beforeEach(() => {
    window.history.replaceState(null, '', '/?mobile=1');
    showStartupView();
    resetAuthSubmissions();
    welcomeSeen.set(false);
    host = document.createElement('div');
    document.body.appendChild(host);
    onBackToPhone = vi.fn<() => void>();
    app = mount(AuthScreens, {
        target: host,
        props: {
            onSetup: vi.fn(),
            onPhone: vi.fn(),
            onCode: vi.fn(),
            onPassword: vi.fn(),
            onBackToPhone,
        },
    });
});

afterEach(async () => {
    showStartupView();
    resetAuthSubmissions();
    flushSync();
    await unmount(app);
    host.remove();
    window.history.replaceState(null, '', '/');
});

describe('AuthScreens on a phone', () => {
    it('opens the welcome page in front of setup and hands over on Continue', async () => {
        showAuthView('setup');
        flushSync();

        expect(host.querySelector('.auth-welcome')).not.toBeNull();
        expect(host.textContent).toContain('Your Telegram, as a drive.');
        expect(host.querySelector('form')).toBeNull();

        host.querySelector<HTMLButtonElement>('.auth-welcome button')?.click();
        flushSync();
        await tick();

        expect(host.querySelector('.auth-welcome')).toBeNull();
        expect(host.querySelector('form[aria-labelledby="auth-setup-title"]')).not.toBeNull();
        expect(document.activeElement).toBe(field('telegram-api-id'));
        expect(field('telegram-api-id').getAttribute('enterkeyhint')).toBe('next');
        expect(host.querySelector('.auth-page-body')).not.toBeNull();
        expect(host.querySelector('.auth-actions button[type="submit"]')).not.toBeNull();
    });

    it('moves the return key from the API ID to the hash instead of submitting', () => {
        showAuthView('setup');
        flushSync();
        host.querySelector<HTMLButtonElement>('.auth-welcome button')?.click();
        flushSync();
        const submit = vi.fn((event: Event) => event.preventDefault());
        host.querySelector('form')?.addEventListener('submit', submit);

        const enter = new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true });
        field('telegram-api-id').dispatchEvent(enter);

        expect(enter.defaultPrevented).toBe(true);
        expect(document.activeElement).toBe(field('telegram-api-hash'));
        expect(submit).not.toHaveBeenCalled();
    });

    it('offers the SMS code on the code step and links the phone step to the dialer keyboard', () => {
        showAuthView('phone');
        flushSync();
        expect(field('telegram-phone').getAttribute('inputmode')).toBe('tel');
        expect(field('telegram-phone').getAttribute('autocomplete')).toBe('tel');
        expect(field('telegram-phone').getAttribute('enterkeyhint')).toBe('send');

        showAuthView('code');
        flushSync();
        expect(field('telegram-code').getAttribute('inputmode')).toBe('numeric');
        expect(field('telegram-code').getAttribute('autocomplete')).toBe('one-time-code');
        expect(field('telegram-code').getAttribute('enterkeyhint')).toBe('go');
    });

    it('skips the welcome page for a device that already has credentials', () => {
        showAuthView('phone');
        flushSync();
        expect(host.querySelector('.auth-welcome')).toBeNull();
        expect(host.querySelector('form[aria-labelledby="auth-phone-title"]')).not.toBeNull();
    });

    it('steps BACK from the code page to the phone page', () => {
        showAuthView('code');
        flushSync();
        expect(closeTopSheet()).toBe(true);
        expect(onBackToPhone).toHaveBeenCalledOnce();
    });

    it('leaves BACK to the host while a code is being checked', () => {
        showAuthView('code');
        beginAuthSubmission('code');
        flushSync();
        expect(hasOpenSheet()).toBe(false);
    });

    it('steps BACK from credentials to the welcome page', () => {
        showAuthView('setup');
        flushSync();
        expect(hasOpenSheet()).toBe(false);
        host.querySelector<HTMLButtonElement>('.auth-welcome button')?.click();
        flushSync();

        expect(closeTopSheet()).toBe(true);
        flushSync();
        expect(host.querySelector('.auth-welcome')).not.toBeNull();
        expect(hasOpenSheet()).toBe(false);
    });
});
