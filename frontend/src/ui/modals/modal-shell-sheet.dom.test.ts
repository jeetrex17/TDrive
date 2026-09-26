// Sheet presentation for ModalShell: the phone chrome (grabber, scrolling
// body), swipe-to-dismiss, and the guarantee that a dialog stays a dialog.
// ModalShell is mounted directly with raw snippets so both presentations can
// be exercised without a per-modal wrapper.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createRawSnippet, flushSync, mount, tick, unmount } from 'svelte';
import ModalShell from './ModalShell.svelte';

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
const actions = createRawSnippet(() => ({
    render: () => `<button id="sheet-primary" class="primary-btn" type="button">Save</button>`,
}));

function mountShell(presentation: 'dialog' | 'sheet', onClose = vi.fn()) {
    app = mount(ModalShell, {
        target: host,
        props: {
            hostId: 'sheet-host',
            open: true,
            title: 'Rename file',
            titleId: 'sheet-title',
            presentation,
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

    it('springs back on a short drag and does not dismiss', () => {
        const onClose = mountShell('sheet');
        const handle = host.querySelector('.sheet-handle') as HTMLElement;

        drag(handle, 0, 12);
        expect(onClose).not.toHaveBeenCalled();
        expect(host.querySelector('.modal-sheet')).not.toBeNull();
    });
});
