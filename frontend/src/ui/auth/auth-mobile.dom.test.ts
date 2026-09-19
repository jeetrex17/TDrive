// Phone-only behaviour of the auth screens: the welcome page, the credential
// Paste buttons, the return-key hand-off, and the tap guards.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, tick, unmount } from 'svelte';
import AuthScreens from './AuthScreens.svelte';
import { showAuthView, showStartupView } from '../app/app-store';
import { resetAuthSubmissions } from './auth-store';

let host: HTMLElement;
let app: Record<string, unknown>;
let readText: ReturnType<typeof vi.fn<() => Promise<string>>>;

function field(id: string): HTMLInputElement {
    const el = host.querySelector<HTMLInputElement>(`#${id}`);
    if (!el) throw new Error(`${id} not rendered`);
    return el;
}

function pasteButton(label: string): HTMLButtonElement {
    const el = host.querySelector<HTMLButtonElement>(`button[aria-label="${label}"]`);
    if (!el) throw new Error(`${label} button not rendered`);
    return el;
}

beforeEach(() => {
    window.history.replaceState(null, '', '/?mobile=1');
    readText = vi.fn<() => Promise<string>>();
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { readText } });
    showStartupView();
    resetAuthSubmissions();
    host = document.createElement('div');
    document.body.appendChild(host);
    app = mount(AuthScreens, {
        target: host,
        props: {
            onSetup: vi.fn(),
            onPhone: vi.fn(),
            onCode: vi.fn(),
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
    Reflect.deleteProperty(navigator, 'clipboard');
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

    it('pastes the clipboard into the tapped credential field', async () => {
        showAuthView('setup');
        flushSync();
        host.querySelector<HTMLButtonElement>('.auth-welcome button')?.click();
        flushSync();

        readText.mockResolvedValueOnce(' 12345678 ');
        pasteButton('Paste API ID').click();
        await vi.waitFor(() => expect(field('telegram-api-id').value).toBe('12345678'));

        readText.mockResolvedValueOnce('0123456789abcdef0123456789abcdef');
        pasteButton('Paste API hash').click();
        await vi.waitFor(() => expect(field('telegram-api-hash').value).toBe('0123456789abcdef0123456789abcdef'));
        expect(document.activeElement).toBe(field('telegram-api-hash'));
    });

    it('leaves the field alone when the clipboard is refused', async () => {
        showAuthView('setup');
        flushSync();
        host.querySelector<HTMLButtonElement>('.auth-welcome button')?.click();
        flushSync();

        readText.mockRejectedValueOnce(new DOMException('denied', 'NotAllowedError'));
        pasteButton('Paste API hash').click();
        await vi.waitFor(() => expect(readText).toHaveBeenCalledOnce());
        await tick();

        expect(field('telegram-api-hash').value).toBe('');
        expect(document.activeElement).toBe(field('telegram-api-hash'));
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
});
