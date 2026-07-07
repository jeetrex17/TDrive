import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import SelectionBar from './SelectionBar.svelte';
import { setSelectionCount } from './selection-bar-store';

let app: Record<string, unknown> | null = null;
let host: HTMLElement | null = null;

function setup(callbacks = {
    onMove: vi.fn(),
    onDelete: vi.fn(),
    onClear: vi.fn(),
}) {
    host = document.createElement('div');
    host.id = 'selection-bar';
    host.setAttribute('role', 'status');
    host.setAttribute('aria-live', 'polite');
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
        expect(host?.querySelector('#selection-count')?.textContent).toBe('1 selected');

        setSelectionCount(3);
        flushSync();
        expect(host?.querySelector('#selection-count')?.textContent).toBe('3 selected');
    });

    it('normalizes invalid counts before rendering', () => {
        setup();

        setSelectionCount(-2);
        flushSync();
        expect(host?.querySelector('#selection-count')?.textContent).toBe('0 selected');

        setSelectionCount(2.9);
        flushSync();
        expect(host?.querySelector('#selection-count')?.textContent).toBe('2 selected');
    });

    it('routes actions through the controller callbacks', () => {
        const callbacks = setup();

        click('#selection-move');
        click('#selection-delete');
        click('#selection-clear');

        expect(callbacks.onMove).toHaveBeenCalledTimes(1);
        expect(callbacks.onDelete).toHaveBeenCalledTimes(1);
        expect(callbacks.onClear).toHaveBeenCalledTimes(1);
    });
});
