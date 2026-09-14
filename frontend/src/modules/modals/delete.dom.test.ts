// A failed single-file delete (e.g. "File not found" because the backend
// already considers the row gone, such as after an external Telegram
// delete) must refresh the file list instead of leaving a stale, re-clickable
// ghost row on screen. The bulk-delete path already refreshed unconditionally;
// this covers the single-item path, which used to return early on failure.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import DeleteModal from '../../ui/modals/DeleteModal.svelte';
import { closeDeleteModalView } from '../../ui/modals/delete-modal-store';

const deleteFileMock = vi.fn();
const appActionMocks = vi.hoisted(() => ({ refreshFiles: vi.fn() }));
vi.mock('../../../bindings/TDrive/app', () => ({
    DeleteFile: (...args: unknown[]) => deleteFileMock(...args),
}));
vi.mock('../drive-data', () => ({
    deleteFolder: vi.fn(),
}));
vi.mock('../app-actions', () => ({ appActions: () => appActionMocks }));

import { confirmDelete, openDeleteModal } from './delete';

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
    host.id = 'delete-modal';
    document.body.appendChild(host);
    app = mount(DeleteModal, { target: host, props: { onConfirm: confirmDelete } });
    flushSync();
});

afterEach(async () => {
    closeDeleteModalView();
    flushSync();
    if (app) await unmount(app);
    app = null;
    host.remove();
    deleteFileMock.mockReset();
    appActionMocks.refreshFiles.mockReset();
});

describe('confirmDelete (single file)', () => {
    it('refreshes the file list when the delete fails', async () => {
        deleteFileMock.mockResolvedValue('Error: File not found');

        openDeleteModal({ type: 'file', id: 42, name: 'ghost.png' });
        flushSync();
        click('#delete-confirm');

        // confirmDelete's error branch is async (await deleteFileWithPasswordRetry).
        await vi.waitFor(() => expect(appActionMocks.refreshFiles).toHaveBeenCalledTimes(1));
    });

    it('still refreshes the file list when the delete succeeds', async () => {
        deleteFileMock.mockResolvedValue('Success');

        openDeleteModal({ type: 'file', id: 43, name: 'real.png' });
        flushSync();
        click('#delete-confirm');

        await vi.waitFor(() => expect(appActionMocks.refreshFiles).toHaveBeenCalledTimes(1));
    });
});
