// Browser behavior for auth forms, value retention, and the appearance control.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, tick, unmount } from 'svelte';
import AuthScreens from './AuthScreens.svelte';
import {
    setPreferredTheme,
    setThemeMode,
    THEME_TRANSITION_CLASS,
    themeController,
} from '../theme/theme-controller';
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

function rect(left: number, top: number, width: number, height: number): DOMRect {
    return {
        x: left,
        y: top,
        left,
        top,
        right: left + width,
        bottom: top + height,
        width,
        height,
        toJSON: () => ({}),
    };
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
    setPreferredTheme('light', 'tdrive-light');
    setPreferredTheme('dark', 'tokyo-night');
    setThemeMode('dark');
    themeController.destroy();
    authScreen.set(null);
    resetAuthSubmissions();
    flushSync();
    await unmount(app);
    host.remove();
});

describe('AuthScreens interactions', () => {
    it('opens and dismisses appearance settings before login', async () => {
        authScreen.set('phone');
        flushSync();

        const trigger = host.querySelector<HTMLButtonElement>('#auth-appearance-trigger');
        trigger?.click();
        flushSync();
        await tick();
        await tick();
        expect(host.querySelector('#auth-appearance-popover')).not.toBeNull();
        expect(trigger?.getAttribute('aria-expanded')).toBe('true');
        expect(host.querySelector('.appearance-back')).toBeNull();
        expect(host.querySelector('[data-appearance-mode="dark"]')).toBe(document.activeElement);

        host.querySelector<HTMLButtonElement>('#appearance-theme-dracula')?.click();
        flushSync();
        await tick();
        host.querySelector<HTMLButtonElement>('#appearance-theme-nord')?.click();
        flushSync();
        await tick();
        expect(host.querySelector('#auth-appearance-popover')).not.toBeNull();
        expect(host.querySelector('#appearance-theme-nord')?.getAttribute('aria-checked')).toBe('true');

        window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
        flushSync();
        expect(host.querySelector('#auth-appearance-popover')).toBeNull();
        expect(trigger?.getAttribute('aria-expanded')).toBe('false');
    });

    it('does not reopen appearance settings after the auth flow unmounts and returns', () => {
        authScreen.set('phone');
        flushSync();
        host.querySelector<HTMLButtonElement>('#auth-appearance-trigger')?.click();
        flushSync();
        expect(host.querySelector('#auth-appearance-popover')).not.toBeNull();

        authScreen.set(null);
        flushSync();
        authScreen.set('setup');
        flushSync();

        expect(host.querySelector('#auth-appearance-popover')).toBeNull();
        expect(host.querySelector('#auth-appearance-trigger')?.getAttribute('aria-expanded')).toBe('false');
    });

    it('recovers a rapid palette click intercepted by the root transition layer', async () => {
        authScreen.set('phone');
        flushSync();
        host.querySelector<HTMLButtonElement>('#auth-appearance-trigger')?.click();
        flushSync();
        await tick();

        const popover = host.querySelector<HTMLElement>('#auth-appearance-popover');
        const nord = host.querySelector<HTMLButtonElement>('#appearance-theme-nord');
        if (!popover || !nord) throw new Error('appearance controls did not render');
        vi.spyOn(popover, 'getBoundingClientRect').mockReturnValue(rect(100, 100, 360, 640));
        vi.spyOn(nord, 'getBoundingClientRect').mockReturnValue(rect(280, 360, 160, 100));
        document.documentElement.classList.add(THEME_TRANSITION_CLASS);

        document.documentElement.dispatchEvent(new MouseEvent('click', {
            bubbles: true,
            cancelable: true,
            clientX: 320,
            clientY: 400,
        }));
        flushSync();
        await tick();

        expect(host.querySelector('#auth-appearance-popover')).not.toBeNull();
        expect(host.querySelector('#appearance-theme-nord')?.getAttribute('aria-checked')).toBe('true');
        expect(document.activeElement).toBe(host.querySelector('#appearance-theme-nord'));
    });

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

describe('AuthScreens drive picker chrome', () => {
    it('keeps the appearance control off the recovery screen', async () => {
        authScreen.set('phone');
        await tick();
        expect(host.querySelector('#auth-appearance-trigger')).not.toBeNull();

        authScreen.set('drive');
        await tick();
        expect(host.querySelector('#auth-appearance-trigger')).toBeNull();
        expect(host.querySelector('#auth-appearance-popover')).toBeNull();
    });
});
