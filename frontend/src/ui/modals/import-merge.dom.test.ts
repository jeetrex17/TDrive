import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import ImportOptionsModal from './ImportOptionsModal.svelte';
import { importOptionsModal, type ImportOptionsPayload } from './import-options-modal-store';

const onConfirm = vi.fn();
let host: HTMLDivElement;
let modal: ReturnType<typeof mount>;

const payload: ImportOptionsPayload = {
    plan: {
        files: 2, folders: 1, bytes: 48, oversize: 0, archives: 0, ignored: 0,
        maxItems: 10_000, limitExceeded: false, maxBytes: 10_000,
    },
    personal: false,
    hasArchives: false,
};

beforeEach(() => {
    host = document.createElement('div');
    document.body.append(host);
    onConfirm.mockClear();
    modal = mount(ImportOptionsModal, {
        target: host,
        props: { onCancel: importOptionsModal.close, onConfirm, onToggle: () => {} },
    });
});

afterEach(async () => {
    importOptionsModal.close();
    await unmount(modal);
    host.remove();
});

function click(selector: string): void {
    const element = host.querySelector<HTMLElement>(selector);
    if (!element) throw new Error(`Missing ${selector}`);
    element.click();
    flushSync();
}

describe('folder merge import options', () => {
    it('opts in to merge and defaults to skipping files with the same name', () => {
        importOptionsModal.open({ ...payload, allowFolderMerge: true });
        flushSync();
        expect(host.querySelector('#import-options-merge')).not.toBeNull();
        expect(host.querySelector('input[name="import-file-conflict"]')).toBeNull();

        click('#import-options-merge');
        expect(host.querySelector<HTMLInputElement>('input[name="import-file-conflict"][value="skip"]')?.checked).toBe(true);
        click('#import-options-confirm');
        expect(onConfirm).toHaveBeenLastCalledWith({ encrypt: false, extract: false, folderMerge: 'skip' });

        click('input[name="import-file-conflict"][value="keep-both"]');
        click('#import-options-confirm');
        expect(onConfirm).toHaveBeenLastCalledWith({ encrypt: false, extract: false, folderMerge: 'keep-both' });
    });

    it('keeps separate imports as the default and omits merge where unavailable', () => {
        importOptionsModal.open({ ...payload, allowFolderMerge: true });
        flushSync();
        click('#import-options-confirm');
        expect(onConfirm).toHaveBeenLastCalledWith({ encrypt: false, extract: false });

        importOptionsModal.close();
        flushSync();
        importOptionsModal.open(payload);
        flushSync();
        expect(host.querySelector('#import-options-merge')).toBeNull();
    });
});
