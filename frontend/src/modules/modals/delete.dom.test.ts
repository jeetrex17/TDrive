// A failed single-file delete (e.g. "File not found" because the backend
// already considers the row gone, such as after an external Telegram
// delete) must refresh the file list instead of leaving a stale, re-clickable
// ghost row on screen. The bulk-delete path already refreshed unconditionally;
// this covers the single-item path, which used to return early on failure.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

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

import { moveToTrash } from './delete';
import { busyRowIds } from '../../ui/file-list/busy-rows';
import { toasts } from '../../ui/notifications/toast-store';
import { get } from 'svelte/store';
import { state } from '../../state';

beforeEach(() => {
    state.activeChannel = { id: 11, title: 'First drive', kind: 'personal' };
});

afterEach(() => {
    deleteFileMock.mockReset();
    restoreMock.mockReset();
    appActionMocks.refreshFiles.mockReset();
    toasts.set([]);
    state.activeChannel = null;
});

describe('moveToTrash', () => {
    it('does not delete a row from another drive', () => {
        void moveToTrash({ type: 'file', id: 42, name: 'first.png', channelId: 22 });

        expect(deleteFileMock).not.toHaveBeenCalled();
        expect(get(toasts).some((toast) => toast.title.includes('Switch back'))).toBe(true);
    });

    it('does not delete a bulk selection with mixed or stale drive identities', () => {
        void moveToTrash({ type: 'bulk', parentId: '', items: [
            { type: 'file', id: 42, name: 'first.png', channelId: 11 },
            { type: 'file', id: 43, name: 'second.png', channelId: 22 },
        ] });

        expect(deleteFileMock).not.toHaveBeenCalled();
        expect(get(toasts).some((toast) => toast.title.includes('Switch back'))).toBe(true);
    });

    it('stops a bulk delete if the active drive switches between items', async () => {
        let finishFirst: (result: unknown) => void = () => {};
        deleteFileMock.mockImplementationOnce(() => new Promise((resolve) => { finishFirst = resolve; }));
        void moveToTrash({ type: 'bulk', parentId: '', items: [
            { type: 'file', id: 42, name: 'first.png', channelId: 11 },
            { type: 'file', id: 43, name: 'second.png', channelId: 11 },
        ] });
        expect(deleteFileMock).toHaveBeenCalledTimes(1);

        state.activeChannel = { id: 22, title: 'Second drive', kind: 'personal' };
        finishFirst({ ok: true });
        await vi.waitFor(() => expect(appActionMocks.refreshFiles).toHaveBeenCalledTimes(1));

        expect(deleteFileMock).toHaveBeenCalledTimes(1);
        expect(get(toasts).some((toast) => toast.title.includes('Switch back'))).toBe(true);
    });

    it('refreshes the file list when the delete fails', async () => {
        deleteFileMock.mockResolvedValue('Error: File not found');

        void moveToTrash({ type: 'file', id: 42, name: 'ghost.png' });

        await vi.waitFor(() => expect(appActionMocks.refreshFiles).toHaveBeenCalledTimes(1));
    });

    it('still refreshes the file list when the delete succeeds', async () => {
        deleteFileMock.mockResolvedValue({ ok: true });

        void moveToTrash({ type: 'file', id: 43, name: 'real.png' });

        await vi.waitFor(() => expect(appActionMocks.refreshFiles).toHaveBeenCalledTimes(1));
    });

    it('moves straight to the Trash and offers Undo', async () => {
        deleteFileMock.mockResolvedValue({ ok: true });

        void moveToTrash({ type: 'file', id: 43, name: 'real.png' });

        await vi.waitFor(() => expect(get(toasts)).toHaveLength(1));
        const [toast] = get(toasts);
        expect(toast.level).toBe('success');
        expect(toast.title).toBe('Moved to Trash');
        expect(toast.action?.label).toBe('Undo');
    });

    it('restores the file from the Trash when Undo is pressed', async () => {
        deleteFileMock.mockResolvedValue({ ok: true });
        restoreMock.mockResolvedValue({ ok: true });

        void moveToTrash({ type: 'file', id: 43, name: 'real.png' });

        await vi.waitFor(() => expect(get(toasts)).toHaveLength(1));
        get(toasts)[0].action?.run();

        // The just-deleted file is addressed in the trash as `f:<msgId>`.
        await vi.waitFor(() => expect(restoreMock).toHaveBeenCalledWith(11, 'f:43'));
        await vi.waitFor(() => expect(appActionMocks.refreshFiles).toHaveBeenCalledTimes(2));
    });

    it('does not redirect the second bulk Undo restore after a drive switch', async () => {
        deleteFileMock.mockResolvedValue({ ok: true });
        let finishFirst: (result: unknown) => void = () => {};
        restoreMock.mockImplementationOnce(() => new Promise((resolve) => { finishFirst = resolve; }));

        void moveToTrash({ type: 'bulk', parentId: '', items: [
            { type: 'file', id: 43, name: 'one.png' },
            { type: 'file', id: 44, name: 'two.png' },
        ] });
        await vi.waitFor(() => expect(deleteFileMock).toHaveBeenCalledTimes(2));
        await vi.waitFor(() => expect(get(toasts)[0]?.action?.label).toBe('Undo'));

        get(toasts)[0].action?.run();
        await vi.waitFor(() => expect(restoreMock).toHaveBeenCalledTimes(1));
        state.activeChannel = { id: 22, title: 'Second drive', kind: 'shared' };
        finishFirst({ ok: true });

        await vi.waitFor(() => expect(get(toasts).some((toast) => toast.title.includes('Switch back'))).toBe(true));
        expect(restoreMock).toHaveBeenCalledTimes(1);
        expect(appActionMocks.refreshFiles).toHaveBeenCalledTimes(1);
    });

    it('still says so when it fails, because nothing else on screen will', async () => {
        deleteFileMock.mockResolvedValue('Error: File not found');

        void moveToTrash({ type: 'file', id: 44, name: 'ghost.png' });

        await vi.waitFor(() => expect(get(toasts)).toHaveLength(1));
        expect(get(toasts)[0].level).toBe('error');
    });

    it('quiets the row while the delete runs, and lets it go afterwards', async () => {
        let settle: (value: unknown) => void = () => {};
        deleteFileMock.mockReturnValue(new Promise((resolve) => { settle = resolve; }));

        void moveToTrash({ type: 'file', id: 45, name: 'slow.png' });

        // The list only refreshes at the end, so without this the row would sit
        // there looking untouched for the whole round-trip.
        await vi.waitFor(() => expect(get(busyRowIds).has('45')).toBe(true));
        settle({ ok: true });
        await vi.waitFor(() => expect(get(busyRowIds).has('45')).toBe(false));
    });

    it('skips items the user did not upload and says so', async () => {
        deleteFileMock.mockResolvedValue({ ok: true });

        void moveToTrash({ type: 'bulk', parentId: '', items: [
            { type: 'file', id: 46, name: 'mine.png' },
            { type: 'file', id: 47, name: 'theirs.png', canDelete: false },
        ] });

        await vi.waitFor(() => expect(get(toasts)[0]?.title).toBe('Moved to Trash'));
        expect(deleteFileMock).toHaveBeenCalledOnce();
        expect(get(toasts)[0].body).toBe("Skipped 1 you didn't upload.");
    });

    it('does nothing but explain when none of the selection can be deleted', () => {
        void moveToTrash({ type: 'bulk', parentId: '', items: [
            { type: 'file', id: 47, name: 'theirs.png', canDelete: false },
        ] });

        expect(deleteFileMock).not.toHaveBeenCalled();
        expect(get(toasts)[0]?.title).toBe('You can only delete items you uploaded');
    });
});
