//
// Browser-mode behavior tests for the shared ModalShell: host visibility,
// backdrop click, Escape, the busy close-guard, and initial focus. Driven
// through RenameModal so the store → shell → a11y wiring is the real one.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import RenameModal from './RenameModal.svelte';
import {
    closeRenameModalView,
    openRenameModalView,
    setRenameModalInFlight,
} from './rename-modal-store';

// modal-a11y only treats elements with a layout box (offsetParent) as
// focusable; happy-dom has no layout, so approximate it with the DOM parent.
Object.defineProperty(HTMLElement.prototype, 'offsetParent', {
    configurable: true,
    get() {
        return this.parentElement;
    },
});

let host: HTMLElement;
let app: Record<string, unknown>;

async function settle(): Promise<void> {
    flushSync();
    // ModalShell activates a11y after tick(); modal-a11y focuses after a rAF.
    await Promise.resolve();
    await new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve)));
    flushSync();
}

function pressEscape(): void {
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }));
    flushSync();
}

beforeEach(() => {
    host = document.createElement('div');
    host.id = 'rename-modal';
    document.body.appendChild(host);
    app = mount(RenameModal, { target: host, props: { onSubmit: vi.fn() } });
});

afterEach(async () => {
    closeRenameModalView();
    flushSync();
    await unmount(app);
    host.remove();
});

describe('ModalShell behavior', () => {
    it('shows and hides the host with the store', async () => {
        expect(host.style.display).not.toBe('flex');

        openRenameModalView({ type: 'file', id: 1, name: 'a.txt' });
        await settle();

        expect(host.style.display).toBe('flex');
        expect(host.getAttribute('aria-hidden')).toBe('false');
        expect(host.querySelector('.modal-card')).not.toBeNull();

        closeRenameModalView();
        flushSync();

        expect(host.style.display).toBe('none');
        expect(host.getAttribute('aria-hidden')).toBe('true');
        expect(host.querySelector('.modal-card')).toBeNull();
    });

    it('closes on backdrop click but not on card click', async () => {
        openRenameModalView({ type: 'file', id: 1, name: 'a.txt' });
        await settle();

        const card = host.querySelector('.modal-card') as HTMLElement;
        card.dispatchEvent(new MouseEvent('click', { bubbles: true }));
        flushSync();
        expect(host.style.display).toBe('flex');

        host.dispatchEvent(new MouseEvent('click', { bubbles: true }));
        flushSync();
        expect(host.style.display).toBe('none');
    });

    it('closes on Escape, but not while a submit is in flight', async () => {
        openRenameModalView({ type: 'file', id: 1, name: 'a.txt' });
        await settle();

        setRenameModalInFlight(true);
        flushSync();
        pressEscape();
        expect(host.style.display).toBe('flex');

        setRenameModalInFlight(false);
        flushSync();
        pressEscape();
        expect(host.style.display).toBe('none');
    });

    it('moves initial focus to the configured control', async () => {
        openRenameModalView({ type: 'file', id: 1, name: 'a.txt' });
        await settle();

        expect((document.activeElement as HTMLElement | null)?.id).toBe('rename-input');
    });
});
