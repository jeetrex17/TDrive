// The context menu renders as a bottom action sheet on a phone, fed by the same
// store: an item header, plain rows, a separated destructive row and an explicit
// Cancel. isMobilePlatform is forced true so the mobile branch renders.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, tick, unmount } from 'svelte';

vi.mock('../../api', async (importOriginal) => {
    const actual = await importOriginal<typeof import('../../api')>();
    return { ...actual, isMobilePlatform: () => true };
});

import ContextMenu from './ContextMenu.svelte';
import { contextMenuState, hideContextMenu, showContextMenu } from './context-menu-store';

Object.defineProperty(HTMLElement.prototype, 'offsetParent', {
    configurable: true,
    get() {
        return this.parentElement;
    },
});

let app: Record<string, unknown> | null = null;
let host: HTMLElement | null = null;

async function settle(): Promise<void> {
    flushSync();
    await tick();
    await Promise.resolve();
    await tick();
    flushSync();
}

function rows(): HTMLButtonElement[] {
    return Array.from(document.querySelectorAll<HTMLButtonElement>('.action-sheet-row'));
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
    contextMenuState.set({ open: false, x: 0, y: 0, items: [], header: null, focusVersion: 0 });
});

describe('ContextMenu action sheet (mobile)', () => {
    it('renders the header, action rows, a separator and Cancel', async () => {
        showContextMenu(0, 0, [
            { label: 'Open', action: vi.fn() },
            { label: 'Download', action: vi.fn() },
            { type: 'divider' },
            { label: 'Delete', danger: true, action: vi.fn() },
        ], { header: { title: 'Brand Guidelines.pdf', meta: '4.2 MB · PDF · Design Assets', kind: 'file' } });
        await settle();

        expect(document.querySelector('.action-sheet')).not.toBeNull();
        expect(document.querySelector('.action-sheet-scrim')).not.toBeNull();
        expect(document.querySelector('.action-sheet-title')?.textContent).toBe('Brand Guidelines.pdf');
        expect(document.querySelector('.action-sheet-meta')?.textContent).toBe('4.2 MB · PDF · Design Assets');

        const items = rows();
        expect(items.map((b) => b.textContent?.trim())).toEqual(['Open', 'Download', 'Delete']);
        expect(items[2].classList.contains('danger')).toBe(true);
        expect(document.querySelectorAll('.action-sheet-sep')).toHaveLength(1);
        // No Cancel row: the handle, a swipe and the scrim already dismiss it,
        // and a fourth way costs a full row at the bottom of the sheet.
        expect(document.querySelector('.action-sheet-cancel')).toBeNull();
        // Not the desktop popover.
        expect(document.querySelector('.context-menu-panel')).toBeNull();
    });

    it('is a named modal dialog for the file its actions affect', async () => {
        showContextMenu(0, 0, [{ label: 'Open', action: vi.fn() }], {
            header: { title: 'Brand Guidelines.pdf', kind: 'file' },
        });
        await settle();

        const actionSheet = document.querySelector<HTMLElement>('.action-sheet');
        const title = document.querySelector<HTMLElement>('.action-sheet-title');
        expect(actionSheet?.getAttribute('role')).toBe('dialog');
        expect(actionSheet?.getAttribute('aria-modal')).toBe('true');
        expect(title?.id).toBe('action-sheet-title');
        expect(actionSheet?.getAttribute('aria-labelledby')).toBe('action-sheet-title');
    });

    it('owns focus while open and returns it when dismissed', async () => {
        const invoker = document.createElement('button');
        invoker.type = 'button';
        document.body.append(invoker);
        invoker.focus();
        showContextMenu(0, 0, [
            { label: 'Open', action: vi.fn() },
            { label: 'Download', action: vi.fn() },
        ], { header: { title: 'a.txt', kind: 'file' } });
        await settle();

        expect(invoker.inert).toBe(true);
        const actions = Array.from(document.querySelectorAll<HTMLButtonElement>('.action-sheet button:not(:disabled)'));
        actions[actions.length - 1]?.focus();
        const tab = new KeyboardEvent('keydown', { key: 'Tab', bubbles: true, cancelable: true });
        document.dispatchEvent(tab);
        expect(tab.defaultPrevented).toBe(true);
        expect(document.activeElement).toBe(actions[0]);

        document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }));
        await settle();
        expect(invoker.inert).toBe(false);
        expect(document.activeElement).toBe(invoker);
        invoker.remove();
    });

    it('runs an item action once and closes', async () => {
        const open = vi.fn();
        showContextMenu(0, 0, [{ label: 'Open', action: open }], { header: { title: 'a.txt', kind: 'file' } });
        await settle();

        rows()[0].dispatchEvent(new MouseEvent('click', { bubbles: true }));
        flushSync();

        expect(open).toHaveBeenCalledTimes(1);
        expect(document.querySelector('.action-sheet')).toBeNull();
    });

    it('closes on the scrim without running an action', async () => {
        // The dimmed screen behind is what a Cancel row used to duplicate, and
        // it is the target a thumb reaches for first.
        const action = vi.fn();
        showContextMenu(0, 0, [{ label: 'Open', action }], { header: { title: 'a.txt', kind: 'file' } });
        await settle();

        (document.querySelector('.action-sheet-scrim') as HTMLElement).dispatchEvent(
            new MouseEvent('click', { bubbles: true }),
        );
        flushSync();

        expect(action).not.toHaveBeenCalled();
        expect(document.querySelector('.action-sheet')).toBeNull();
    });
});
