// Behavior tests for the promise plumbing behind the upload-options dialog:
// open resolves with the choice on Continue and null on Cancel, and a second
// open while one is pending joins the same visible prompt instead of
// stranding either caller.

import { afterEach, beforeAll, describe, expect, it } from 'vitest';
import { flushSync } from 'svelte';
import { openUploadOptionsModal, setupUploadOptionsModal } from './upload-options';
import { uploadOptionsModal } from '../../ui/modals/upload-options-modal-store';

let host: HTMLElement;

function click(selector: string): void {
    const el = host.querySelector(selector) as HTMLElement | null;
    if (!el) throw new Error(`missing ${selector}`);
    el.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    flushSync();
}

// setup mounts once for the app lifetime, so the fixture mirrors that:
// one host and one setup call shared by every test in this file.
beforeAll(() => {
    host = document.createElement('div');
    host.id = 'upload-options-modal';
    document.body.appendChild(host);
    setupUploadOptionsModal();
});

afterEach(() => {
    uploadOptionsModal.close();
    flushSync();
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
