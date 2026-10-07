// The logout confirm follows the selected option: a quick logout keeps the
// plain primary style, while the reset choice (which wipes local data) switches
// to the real danger button and the fuller label.

import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import LogoutModal from './LogoutModal.svelte';
import { logoutModal } from './logout-modal-store';

let host: HTMLElement;
let app: Record<string, unknown> | null = null;

beforeEach(() => {
    host = document.createElement('div');
    host.id = 'logout-modal';
    document.body.appendChild(host);
    app = mount(LogoutModal, { target: host, props: { onConfirm: () => {} } });
    logoutModal.open(null);
    flushSync();
});

afterEach(async () => {
    logoutModal.close();
    flushSync();
    if (app) await unmount(app);
    app = null;
    host.remove();
});

function confirm(): HTMLButtonElement {
    const el = host.querySelector('#logout-confirm') as HTMLButtonElement | null;
    if (!el) throw new Error('missing #logout-confirm');
    return el;
}

function selectMode(value: string): void {
    const radio = host.querySelector(`input[name="logout-mode"][value="${value}"]`) as HTMLInputElement | null;
    if (!radio) throw new Error(`missing radio ${value}`);
    radio.checked = true;
    radio.dispatchEvent(new Event('change', { bubbles: true }));
    radio.dispatchEvent(new Event('input', { bubbles: true }));
    flushSync();
}

describe('LogoutModal confirm styling', () => {
    it('starts as a plain primary quick logout', () => {
        const btn = confirm();
        expect(btn.classList.contains('primary-btn')).toBe(true);
        expect(btn.classList.contains('danger-btn')).toBe(false);
        expect(btn.textContent?.trim()).toBe('Log out');
    });

    it('becomes a danger action when reset is selected', () => {
        selectMode('full');
        const btn = confirm();
        expect(btn.classList.contains('danger-btn')).toBe(true);
        expect(btn.textContent?.trim()).toBe('Log out and reset');
    });
});
