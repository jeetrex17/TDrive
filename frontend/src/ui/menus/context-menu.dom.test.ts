import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, tick, unmount } from 'svelte';
import ContextMenu from './ContextMenu.svelte';
import { contextMenuState, hideContextMenu, showContextMenu } from './context-menu-store';

let app: Record<string, unknown> | null = null;
let host: HTMLElement | null = null;

async function settle(): Promise<void> {
    flushSync();
    await tick();
    await Promise.resolve();
    await tick();
    await Promise.resolve();
    flushSync();
}

function buttons(): HTMLButtonElement[] {
    return Array.from(document.querySelectorAll<HTMLButtonElement>('.context-menu-panel button[role="menuitem"]'));
}

async function openMenu(): Promise<{ first: ReturnType<typeof vi.fn>; second: ReturnType<typeof vi.fn> }> {
    const first = vi.fn();
    const second = vi.fn();

    showContextMenu(24, 32, [
        { label: 'Open', action: first },
        { label: 'Rename', action: second },
    ]);
    await settle();
    return { first, second };
}

beforeEach(() => {
    host = document.createElement('div');
    document.body.appendChild(host);
    app = mount(ContextMenu, { target: host, props: {} });
});

afterEach(async () => {
    hideContextMenu();
    flushSync();
    if (app) await unmount(app);
    host?.remove();
    app = null;
    host = null;
    contextMenuState.set({
        open: false,
        x: 0,
        y: 0,
        items: [],
        focusVersion: 0,
    });
});

describe('ContextMenu', () => {
    it('opens at the requested point and focuses the first enabled item', async () => {
        await openMenu();

        const panel = document.querySelector<HTMLElement>('.context-menu-panel');
        expect(panel).not.toBeNull();
        expect(panel?.getAttribute('role')).toBe('menu');
        expect(panel?.style.left).toBe('24px');
        expect(panel?.style.top).toBe('32px');
        expect(document.activeElement).toBe(buttons()[0]);
    });

    it('supports arrow, edge, and Escape keyboard behavior', async () => {
        await openMenu();
        const [first, second] = buttons();

        document.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true, cancelable: true }));
        expect(document.activeElement).toBe(second);

        document.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true, cancelable: true }));
        expect(document.activeElement).toBe(first);

        document.dispatchEvent(new KeyboardEvent('keydown', { key: 'End', bubbles: true, cancelable: true }));
        expect(document.activeElement).toBe(second);

        document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Home', bubbles: true, cancelable: true }));
        expect(document.activeElement).toBe(first);

        document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }));
        flushSync();
        expect(document.querySelector('.context-menu-panel')).toBeNull();
    });

    it('invokes item actions once and closes before running the action', async () => {
        const { first } = await openMenu();

        buttons()[0]?.dispatchEvent(new MouseEvent('click', { bubbles: true }));
        flushSync();

        expect(first).toHaveBeenCalledTimes(1);
        expect(document.querySelector('.context-menu-panel')).toBeNull();
    });

    it('closes on outside click but ignores clicks inside the panel', async () => {
        await openMenu();

        buttons()[0]?.dispatchEvent(new MouseEvent('click', { bubbles: true }));
        flushSync();
        // Re-open because the click above deliberately invoked and closed.
        await openMenu();

        const panel = document.querySelector<HTMLElement>('.context-menu-panel');
        panel?.dispatchEvent(new MouseEvent('click', { bubbles: true }));
        flushSync();
        expect(document.querySelector('.context-menu-panel')).not.toBeNull();

        document.dispatchEvent(new MouseEvent('click', { bubbles: true }));
        flushSync();
        expect(document.querySelector('.context-menu-panel')).toBeNull();
    });
});
