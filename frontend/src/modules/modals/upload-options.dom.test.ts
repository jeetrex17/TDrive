// Behavior tests for the promise plumbing behind the upload-options dialog:
// open resolves with the choice on Continue and null on Cancel, and a second
// open while one is pending joins the same visible prompt instead of
// stranding either caller.

import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import UploadOptionsModal from '../../ui/modals/UploadOptionsModal.svelte';
import { uploadOptionsModal } from '../../ui/modals/upload-options-modal-store';
import {
    cancelUploadOptions,
    confirmUploadOptions,
    openUploadOptionsModal,
} from './upload-options';

let host: HTMLElement;
let app: Record<string, unknown> | null = null;

function click(selector: string): void {
    const el = host.querySelector(selector) as HTMLElement | null;
    if (!el) throw new Error(`missing ${selector}`);
    el.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    flushSync();
}

beforeEach(() => {
    host = document.createElement('div');
    host.id = 'upload-options-modal';
    document.body.appendChild(host);
    app = mount(UploadOptionsModal, {
        target: host,
        props: { onCancel: cancelUploadOptions, onConfirm: confirmUploadOptions },
    });
    flushSync();
});

afterEach(async () => {
    cancelUploadOptions();
    uploadOptionsModal.close();
    flushSync();
    if (app) await unmount(app);
    app = null;
    host.remove();
});

describe('openUploadOptionsModal', () => {
    it('resolves with the plain choice on Continue', async () => {
        const choice = openUploadOptionsModal({ count: 2 });
        flushSync();

        expect(host.style.display).toBe('flex');
        click('#upload-options-confirm');

        await expect(choice).resolves.toEqual({ encrypt: false });
        expect(host.style.display).toBe('none');
    });

    it('resolves with the encrypted choice when selected', async () => {
        const choice = openUploadOptionsModal({ count: 1 });
        flushSync();

        const encrypt = host.querySelector('input[value="encrypt"]') as HTMLInputElement;
        encrypt.click();
        flushSync();
        click('#upload-options-confirm');

        await expect(choice).resolves.toEqual({ encrypt: true });
    });

    it('resolves null on cancel', async () => {
        const choice = openUploadOptionsModal({ count: 1 });
        flushSync();
        click('#upload-options-cancel');

        await expect(choice).resolves.toBeNull();
    });

    it('joins a second caller onto the pending prompt', async () => {
        const first = openUploadOptionsModal({ count: 1 });
        flushSync();
        const second = openUploadOptionsModal({ count: 5 });
        flushSync();

        click('#upload-options-confirm');

        await expect(first).resolves.toEqual({ encrypt: false });
        await expect(second).resolves.toEqual({ encrypt: false });
    });
});
