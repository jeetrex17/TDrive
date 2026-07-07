// Behavior tests for the two field-reset paths that needed care: clearing on a
// screen transition, and clearing via the reset nonce when a rejected code
// keeps the user on the same screen (where a screen-value change cannot fire).

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import AuthScreens from './AuthScreens.svelte';
import { authCodeReset, authPhone, authScreen } from './auth-store';

let host: HTMLElement;
let app: Record<string, unknown>;

function codeInput(): HTMLInputElement {
    const el = host.querySelector('.code-input') as HTMLInputElement | null;
    if (!el) throw new Error('code input not rendered');
    return el;
}

beforeEach(() => {
    authScreen.set(null);
    authPhone.set('');
    authCodeReset.set(0);
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
    authScreen.set(null);
    flushSync();
    await unmount(app);
    host.remove();
});

describe('AuthScreens field reset', () => {
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

    it('clears the code field on a reset nonce bump without a screen change', () => {
        authScreen.set('code');
        flushSync();
        const input = codeInput();
        input.value = '999';
        input.dispatchEvent(new Event('input', { bubbles: true }));
        flushSync();
        expect(codeInput().value).toBe('999');

        // Same screen, rejected code: only the nonce changes.
        authCodeReset.update((n) => n + 1);
        flushSync();

        expect(codeInput().value).toBe('');
    });
});
