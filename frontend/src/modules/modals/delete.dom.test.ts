// A failed single-file delete (e.g. "File not found" because the backend
// already considers the row gone, such as after an external Telegram
// delete) must refresh the file list instead of leaving a stale, re-clickable
// ghost row on screen. The bulk-delete path already refreshed unconditionally;
// this covers the single-item path, which used to return early on failure.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import DeleteModal from '../../ui/modals/DeleteModal.svelte';
import { closeDeleteModalView, deleteModalState } from '../../ui/modals/delete-modal-store';

const deleteFileMock = vi.fn();
const restoreMock = vi.fn();
const appActionMocks = vi.hoisted(() => ({ refreshFiles: vi.fn() }));
vi.mock('../../../bindings/TDrive/internal/app/app', () => ({
    DeleteFile: (...args: unknown[]) => deleteFileMock(...args),
    RestoreFromTrash: (...args: unknown[]) => restoreMock(...args),
}));
vi.mock('../drive-data', () => ({
    deleteFolder: vi.fn(),
}));
vi.mock('../app-actions', () => ({ appActions: () => appActionMocks }));

import { confirmDelete, openDeleteModal } from './delete';
import { busyRowIds } from '../../ui/file-list/busy-rows';
import { toasts } from '../../ui/notifications/toast-store';
import { get } from 'svelte/store';

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
    restoreMock.mockReset();
    appActionMocks.refreshFiles.mockReset();
    toasts.set([]);
});

describe('openDeleteModal copy', () => {
    it('describes moving a file to the Trash, not deleting from Telegram', () => {
        openDeleteModal({ type: 'file', id: 1, name: 'a.png' });
        const view = get(deleteModalState);
        expect(view.title).toBe('Move file to Trash?');
        expect(view.confirmLabel).toBe('Move to Trash');
        expect(view.subtitle).toContain('Trash');
        expect(view.subtitle).not.toContain('Telegram');
        expect(view.subtitle.toLowerCase()).not.toContain("can't be undone");
    });

    it('describes moving a folder and its contents to the Trash', () => {
        openDeleteModal({ type: 'folder', id: 'd:1', name: 'Docs' });
        const view = get(deleteModalState);
        expect(view.title).toBe('Move folder to Trash?');
        expect(view.subtitle).toContain('everything inside it');
        expect(view.subtitle).toContain('Trash');
    });
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
        deleteFileMock.mockResolvedValue({ ok: true });

        openDeleteModal({ type: 'file', id: 43, name: 'real.png' });
        flushSync();
        click('#delete-confirm');

        await vi.waitFor(() => expect(appActionMocks.refreshFiles).toHaveBeenCalledTimes(1));
    });

    it('confirms the move to Trash on success', async () => {
        deleteFileMock.mockResolvedValue({ ok: true });

        openDeleteModal({ type: 'file', id: 43, name: 'real.png' });
        flushSync();
        click('#delete-confirm');

        await vi.waitFor(() => expect(get(toasts)).toHaveLength(1));
        const [toast] = get(toasts);
        expect(toast.level).toBe('success');
        expect(toast.title).toBe('Moved to Trash');
        expect(toast.action?.label).toBe('Undo');
    });

    it('restores the file from the Trash when Undo is pressed', async () => {
        deleteFileMock.mockResolvedValue({ ok: true });
        restoreMock.mockResolvedValue({ ok: true });

        openDeleteModal({ type: 'file', id: 43, name: 'real.png' });
        flushSync();
        click('#delete-confirm');

        await vi.waitFor(() => expect(get(toasts)).toHaveLength(1));
        get(toasts)[0].action?.run();

        // The just-deleted file is addressed in the trash as `f:<msgId>`.
        await vi.waitFor(() => expect(restoreMock).toHaveBeenCalledWith('f:43'));
        await vi.waitFor(() => expect(appActionMocks.refreshFiles).toHaveBeenCalledTimes(2));
    });

    it('still says so when it fails, because nothing else on screen will', async () => {
        deleteFileMock.mockResolvedValue('Error: File not found');

        openDeleteModal({ type: 'file', id: 44, name: 'ghost.png' });
        flushSync();
        click('#delete-confirm');

        await vi.waitFor(() => expect(get(toasts)).toHaveLength(1));
        expect(get(toasts)[0].level).toBe('error');
    });

    it('quiets the row while the delete runs, and lets it go afterwards', async () => {
        let settle: (value: unknown) => void = () => {};
        deleteFileMock.mockReturnValue(new Promise((resolve) => { settle = resolve; }));

        openDeleteModal({ type: 'file', id: 45, name: 'slow.png' });
        flushSync();
        click('#delete-confirm');

        // The list only refreshes at the end, so without this the row would sit
        // there looking untouched for the whole round-trip.
        await vi.waitFor(() => expect(get(busyRowIds).has('45')).toBe(true));
        settle({ ok: true });
        await vi.waitFor(() => expect(get(busyRowIds).has('45')).toBe(false));
    });
});
