// A failed single-file delete (e.g. "File not found" because the backend
// already considers the row gone, such as after an external Telegram
// delete) must refresh the file list instead of leaving a stale, re-clickable
// ghost row on screen. The bulk-delete path already refreshed unconditionally;
// this covers the single-item path, which used to return early on failure.

import { afterEach, beforeAll, describe, expect, it, vi } from 'vitest';
import { flushSync } from 'svelte';

const deleteFileMock = vi.fn();
vi.mock('../../../wailsjs/go/main/App', () => ({
    DeleteFile: (...args: unknown[]) => deleteFileMock(...args),
}));
vi.mock('../drive-data', () => ({
    deleteFolder: vi.fn(),
}));

import { openDeleteModal, setupDeleteModal } from './delete';

let host: HTMLElement;

function click(selector: string): void {
    const el = host.querySelector(selector) as HTMLElement | null;
    if (!el) throw new Error(`missing ${selector}`);
    el.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    flushSync();
}

beforeAll(() => {
    host = document.createElement('div');
    host.id = 'delete-modal';
    document.body.appendChild(host);
    setupDeleteModal();
});

afterEach(() => {
    deleteFileMock.mockReset();
    (window as any).refreshFiles = undefined;
});

describe('confirmDelete (single file)', () => {
    it('refreshes the file list when the delete fails', async () => {
        deleteFileMock.mockResolvedValue('Error: File not found');
        const refreshFiles = vi.fn();
        (window as any).refreshFiles = refreshFiles;

        openDeleteModal({ type: 'file', id: 42, name: 'ghost.png' });
        flushSync();
        click('#delete-confirm');

        // confirmDelete's error branch is async (await deleteFileWithPasswordRetry).
        await vi.waitFor(() => expect(refreshFiles).toHaveBeenCalledTimes(1));
    });

    it('still refreshes the file list when the delete succeeds', async () => {
        deleteFileMock.mockResolvedValue('Success');
        const refreshFiles = vi.fn();
        (window as any).refreshFiles = refreshFiles;

        openDeleteModal({ type: 'file', id: 43, name: 'real.png' });
        flushSync();
        click('#delete-confirm');

        await vi.waitFor(() => expect(refreshFiles).toHaveBeenCalledTimes(1));
    });
});
