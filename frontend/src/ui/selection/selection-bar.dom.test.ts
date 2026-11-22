import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import SelectionBar from './SelectionBar.svelte';
import { setSelectionCount } from './selection-bar-store';

let app: Record<string, unknown> | null = null;
let host: HTMLElement | null = null;

function setup(callbacks = {
    onDownload: vi.fn(),
    onMove: vi.fn(),
    onDelete: vi.fn(),
    onClear: vi.fn(),
}) {
    host = document.createElement('div');
    host.id = 'selection-bar';
    document.body.appendChild(host);
    app = mount(SelectionBar, { target: host, props: callbacks });
    flushSync();
    return callbacks;
}

function click(selector: string): void {
    const button = host?.querySelector<HTMLButtonElement>(selector);
    if (!button) throw new Error(`missing ${selector}`);
    button.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    flushSync();
}

afterEach(async () => {
    setSelectionCount(0);
    flushSync();
    if (app) await unmount(app);
    host?.remove();
    app = null;
    host = null;
});

describe('SelectionBar', () => {
    it('renders singular and plural counts from the store', () => {
        setup();

        setSelectionCount(1);
        flushSync();
        expect(host?.querySelector('#selection-count')?.textContent?.trim()).toBe('1 selected');

        setSelectionCount(3);
        flushSync();
        expect(host?.querySelector('#selection-count')?.textContent?.trim()).toBe('3 selected');
    });

    it('adds the total size when every selected item is a file', () => {
        setup();

        setSelectionCount(3, 19_500_000);
        flushSync();
        expect(host?.querySelector('#selection-count')?.textContent?.trim()).toBe('3 selected · 18.6 MB');
        expect(host?.querySelector('#selection-count')?.getAttribute('role')).toBe('status');

        setSelectionCount(2, null);
        flushSync();
        expect(host?.querySelector('#selection-count')?.textContent?.trim()).toBe('2 selected');
    });

    it('names its icon-only actions', () => {
        setup();
        for (const name of ['Download', 'Move', 'Delete', 'Clear selection']) {
            expect(host?.querySelector(`button[aria-label="${name}"]`)).not.toBeNull();
        }
    });

    it('normalizes invalid counts before rendering', () => {
        setup();

        setSelectionCount(-2);
        flushSync();
        expect(host?.querySelector('#selection-count')?.textContent?.trim()).toBe('0 selected');

        setSelectionCount(2.9);
        flushSync();
        expect(host?.querySelector('#selection-count')?.textContent?.trim()).toBe('2 selected');
    });

    it('routes actions through the controller callbacks', () => {
        const callbacks = setup();

        click('#selection-move');
        click('#selection-download');
        click('#selection-delete');
        click('#selection-clear');

        expect(callbacks.onMove).toHaveBeenCalledTimes(1);
        expect(callbacks.onDownload).toHaveBeenCalledTimes(1);
        expect(callbacks.onDelete).toHaveBeenCalledTimes(1);
        expect(callbacks.onClear).toHaveBeenCalledTimes(1);
    });

    it('carries a labelled glyph on Download, Move, and Delete for the mobile action bar', () => {
        setup();

        for (const id of ['#selection-download', '#selection-move', '#selection-delete']) {
            const button = host?.querySelector<HTMLButtonElement>(id);
            expect(button?.querySelector('.selection-btn-icon')).not.toBeNull();
            expect(button?.querySelector('.selection-btn-label')?.textContent).toBeTruthy();
        }
    });
});
