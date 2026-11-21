// Sheet presentation for ModalShell: the phone chrome (grabber, scrolling
// body), swipe-to-dismiss, and the guarantee that a dialog stays a dialog.
// ModalShell is mounted directly with raw snippets so both presentations can
// be exercised without a per-modal wrapper.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createRawSnippet, flushSync, mount, tick, unmount } from 'svelte';
import { fromStore, writable } from 'svelte/store';
import ModalShell from './ModalShell.svelte';
import { closeTopSheet } from './sheet-stack';

// modal-a11y only treats elements with a layout box as focusable; happy-dom has
// no layout, so approximate offsetParent with the DOM parent.
Object.defineProperty(HTMLElement.prototype, 'offsetParent', {
    configurable: true,
    get() {
        return this.parentElement;
    },
});

let host: HTMLElement;
let app: Record<string, unknown> | null = null;

const children = createRawSnippet(() => ({
    render: () => `<input id="sheet-input" />`,
}));
const headerWithButton = createRawSnippet(() => ({
    render: () => `<div class="move-modal-header">`
        + `<h3 id="sheet-title" class="modal-title">Move item</h3>`
        + `<button id="header-btn" type="button">Back</button></div>`,
}));
const actions = createRawSnippet(() => ({
    render: () => `<button id="sheet-primary" class="primary-btn" type="button">Save</button>`,
}));

function mountShell(
    presentation: 'dialog' | 'sheet',
    onClose = vi.fn(),
    canDismiss = () => true,
    isOpen = () => true,
) {
    app = mount(ModalShell, {
        target: host,
        props: {
            hostId: 'sheet-host',
            get open() { return isOpen(); },
            title: 'Rename file',
            titleId: 'sheet-title',
            presentation,
            get dismissible() { return canDismiss(); },
            initialFocus: '#sheet-input',
            onClose,
            children,
            actions,
        },
    });
    flushSync();
    return onClose;
}

function drag(handle: HTMLElement, from: number, to: number): void {
    handle.dispatchEvent(new MouseEvent('pointerdown', { clientY: from, bubbles: true }));
    handle.dispatchEvent(new MouseEvent('pointermove', { clientY: to, bubbles: true }));
    handle.dispatchEvent(new MouseEvent('pointerup', { clientY: to, bubbles: true }));
    flushSync();
}

function touchAt(target: HTMLElement, type: string, y: number): Event {
    const point = { clientX: 50, clientY: y, identifier: 1, target } as unknown as Touch;
    const event = new Event(type, { bubbles: true, cancelable: true });
    Object.defineProperties(event, {
        touches: { value: type === 'touchend' ? [] : [point] },
        changedTouches: { value: [point] },
    });
    target.dispatchEvent(event);
    return event;
}

/** Start, one move and a release; returns the move so callers can see if it was claimed. */
function touchDrag(target: HTMLElement, from: number, to: number): Event {
    touchAt(target, 'touchstart', from);
    const move = touchAt(target, 'touchmove', to);
    touchAt(target, 'touchend', to);
    flushSync();
    return move;
}

beforeEach(() => {
    host = document.createElement('div');
    host.id = 'sheet-host';
    document.body.appendChild(host);
});

afterEach(async () => {
    if (app) await unmount(app);
    app = null;
    host.remove();
});

describe('ModalShell sheet presentation', () => {
    it('renders sheet chrome when presentation is sheet', async () => {
        mountShell('sheet');
        await tick();

        expect(host.querySelector('.modal-sheet')).not.toBeNull();
        expect(host.querySelector('.sheet-handle')).not.toBeNull();
        // Children scroll inside their own region; actions stay outside it.
        const body = host.querySelector('.modal-sheet-body');
        expect(body).not.toBeNull();
        expect(body?.querySelector('#sheet-input')).not.toBeNull();
        expect(host.querySelector('.modal-actions #sheet-primary')).not.toBeNull();
    });

    it('keeps a dialog flat: no grabber, no scroll body', () => {
        mountShell('dialog');

        expect(host.querySelector('.modal-sheet')).toBeNull();
        expect(host.querySelector('.sheet-handle')).toBeNull();
        expect(host.querySelector('.modal-sheet-body')).toBeNull();
        // The card and its content still render for a dialog.
        expect(host.querySelector('.modal-card #sheet-input')).not.toBeNull();
    });

    it('dismisses on a downward swipe past the threshold', async () => {
        const onClose = mountShell('sheet');
        const handle = host.querySelector('.sheet-handle') as HTMLElement;

        drag(handle, 0, 320);
        await vi.waitFor(() => expect(onClose).toHaveBeenCalledTimes(1));
    });

    it('keeps a busy sheet in place when dismissal is disabled', async () => {
        const onClose = mountShell('sheet', vi.fn(), () => false);
        await tick();

        const sheet = host.querySelector<HTMLElement>('.modal-sheet')!;
        const handle = host.querySelector<HTMLElement>('.sheet-handle')!;
        host.dispatchEvent(new MouseEvent('click', { bubbles: true }));
        document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }));
        expect(closeTopSheet()).toBe(true);
        drag(handle, 0, 320);

        expect(onClose).not.toHaveBeenCalled();
        expect(sheet.style.transform).toBe('');
        expect(host.style.getPropertyValue('--sheet-scrim')).toBe('1');
    });

    it('cancels a drag when the sheet becomes busy', async () => {
        const enabled = fromStore(writable(true));
        const onClose = mountShell('sheet', vi.fn(), () => enabled.current);
        const sheet = host.querySelector<HTMLElement>('.modal-sheet')!;
        const handle = host.querySelector<HTMLElement>('.sheet-handle')!;

        handle.dispatchEvent(new MouseEvent('pointerdown', { clientY: 0, bubbles: true }));
        handle.dispatchEvent(new MouseEvent('pointermove', { clientY: 160, bubbles: true }));
        expect(sheet.style.transform).toBe('translateY(160px)');

        enabled.current = false;
        flushSync();
        await vi.waitFor(() => expect(sheet.style.transform).toBe(''));
        expect(host.style.getPropertyValue('--sheet-scrim')).toBe('1');

        enabled.current = true;
        flushSync();
        handle.dispatchEvent(new MouseEvent('pointermove', { clientY: 320, bubbles: true }));
        expect(sheet.style.transform).toBe('');
        expect(onClose).not.toHaveBeenCalled();
    });

    it('clears gesture styles when the sheet closes during a drag', () => {
        const opened = fromStore(writable(true));
        mountShell('sheet', vi.fn(), undefined, () => opened.current);
        const sheet = host.querySelector<HTMLElement>('.modal-sheet')!;
        const handle = host.querySelector<HTMLElement>('.sheet-handle')!;

        handle.dispatchEvent(new MouseEvent('pointerdown', { clientY: 0, bubbles: true }));
        handle.dispatchEvent(new MouseEvent('pointermove', { clientY: 160, bubbles: true }));
        expect(sheet.style.transform).toBe('translateY(160px)');

        opened.current = false;
        flushSync();
        expect(sheet.style.transform).toBe('');
        expect(sheet.style.transition).toBe('');
    });

    it('returns the sheet to rest when the close callback leaves it open', async () => {
        const onClose = mountShell('sheet');
        const sheet = host.querySelector<HTMLElement>('.modal-sheet')!;
        const handle = host.querySelector<HTMLElement>('.sheet-handle')!;

        drag(handle, 0, 320);
        await vi.waitFor(() => {
            expect(onClose).toHaveBeenCalledTimes(1);
            expect(sheet.style.transform).toBe('');
            expect(host.style.getPropertyValue('--sheet-scrim')).toBe('1');
        });
    });

    it('dismisses on a pull down from the body when it is scrolled to the top', async () => {
        const onClose = mountShell('sheet');
        const body = host.querySelector('.modal-sheet-body') as HTMLElement;

        touchDrag(body, 100, 420);
        await vi.waitFor(() => expect(onClose).toHaveBeenCalledTimes(1));
    });

    it('lets a scrolled body scroll instead of dragging the sheet', () => {
        const onClose = mountShell('sheet');
        const body = host.querySelector('.modal-sheet-body') as HTMLElement;
        body.scrollTop = 120;

        const move = touchDrag(body, 100, 420);
        expect(move.defaultPrevented).toBe(false);
        expect(onClose).not.toHaveBeenCalled();
    });

    it('springs back on a short drag and does not dismiss', () => {
        const onClose = mountShell('sheet');
        const handle = host.querySelector('.sheet-handle') as HTMLElement;

        drag(handle, 0, 12);
        expect(onClose).not.toHaveBeenCalled();
        expect(host.querySelector('.modal-sheet')).not.toBeNull();
    });

    it('drags from the title row, not just the grip', async () => {
        const onClose = mountShell('sheet');
        const title = host.querySelector('.modal-title') as HTMLElement;
        expect(title).not.toBeNull();

        drag(title, 0, 320);
        await vi.waitFor(() => expect(onClose).toHaveBeenCalledTimes(1));
    });

    it('does not start a drag from a button in the header', () => {
        const onClose = vi.fn();
        app = mount(ModalShell, {
            target: host,
            props: {
                hostId: 'sheet-host',
                open: true,
                titleId: 'sheet-title',
                presentation: 'sheet',
                onClose,
                header: headerWithButton,
                children,
                actions,
            },
        });
        flushSync();

        const button = host.querySelector('#header-btn') as HTMLElement;
        drag(button, 0, 320);
        // The press belonged to the button, so the sheet never left.
        expect(onClose).not.toHaveBeenCalled();
        expect(host.querySelector('.modal-sheet')).not.toBeNull();
    });
});
