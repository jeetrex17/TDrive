import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import { get } from 'svelte/store';
import type { FileItem, FolderContents, FolderStat, RootFile } from '../types';

const api = vi.hoisted(() => ({
    isMobilePlatform: () => false, isIOSPlatform: () => false, isAndroidPlatform: () => false,
    onRuntimeEvent: () => () => {},
    getFolderContents: vi.fn<() => Promise<FolderContents>>(),
    getFileList: vi.fn<() => Promise<RootFile[]>>(),
    getAllFsMsgIds: vi.fn<() => Promise<number[]>>(),
    getStorageUsed: vi.fn<() => Promise<number>>(),
}));
const deps = vi.hoisted(() => ({
    stats: vi.fn<() => Promise<Map<string, FolderStat>>>(),
    ensureUsers: vi.fn<() => Promise<void>>(),
    chip: vi.fn<() => string | null>(),
    offline: vi.fn(() => false),
    refresh: vi.fn(), upload: vi.fn(), gallery: vi.fn(), trash: vi.fn(),
}));
vi.mock('../api', () => api);
vi.mock('./connectivity', () => ({ isOffline: deps.offline }));
vi.mock('./drive-data', () => ({ calculateVisibleFolderStats: deps.stats }));
vi.mock('./uploaders', () => ({ ensureUserNames: deps.ensureUsers, uploaderChipLabel: deps.chip }));
vi.mock('./app-actions', () => ({ appActions: () => ({ refreshFiles: deps.refresh }) }));
vi.mock('./transfers', () => ({ chooseFilesForCurrentFolder: deps.upload, chooseFolderForCurrentFolder: vi.fn(), enqueueDownload: vi.fn(), enqueueFolderDownload: vi.fn() }));
vi.mock('./gallery', () => ({ renderGallery: deps.gallery, setPhotosMode: vi.fn() }));
vi.mock('./trash/view', () => ({ renderTrashRows: deps.trash, setTrashMode: vi.fn() }));
vi.mock('./navigation', () => ({ navigateToFolder: vi.fn() }));
vi.mock('./modals/rename', () => ({ openRenameModal: vi.fn() }));
vi.mock('./modals/delete', () => ({ openDeleteModal: vi.fn() }));
vi.mock('./modals/folder', () => ({ openNewFolderModal: vi.fn() }));
vi.mock('./modals/move', () => ({ openMoveModal: vi.fn() }));
vi.mock('./context-menu', () => ({ showRowContextMenu: vi.fn() }));

import FileList from '../ui/file-list/FileList.svelte';
import { fileListView } from '../ui/file-list/file-list-store';
import { state } from '../state';
import { openNewFolderModal } from './modals/folder';
import { canOwnerActOnFile, buildFileRow, refreshFiles, resetFileListScrollRestore } from './file-list';

let list: HTMLDivElement;
let storage: HTMLDivElement;
let app: ReturnType<typeof mount>;
const file = (name = 'report.txt', msgId = 41): FileItem => ({
    msgId, name, size: 1000, parentId: '', uploadTime: 100, uploaderId: 7,
    encrypted: false, plaintextSize: 0, revision: 1,
});
function deferred<T>() {
    let resolve!: (value: T) => void;
    let reject!: (reason: Error) => void;
    const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
    return { promise, resolve, reject };
}
async function expectText(text: string) {
    await vi.waitFor(() => { flushSync(); expect(list.textContent).toContain(text); });
}

beforeEach(() => {
    vi.resetAllMocks();
    api.getFolderContents.mockResolvedValue({ folders: [], files: [file()] });
    api.getFileList.mockResolvedValue([]);
    api.getAllFsMsgIds.mockResolvedValue([]);
    api.getStorageUsed.mockResolvedValue(1024);
    deps.stats.mockResolvedValue(new Map());
    deps.ensureUsers.mockResolvedValue();
    deps.chip.mockReturnValue(null);
    deps.offline.mockReturnValue(false);
    resetFileListScrollRestore();
    state.activeChannel = { id: 1, title: 'Drive', kind: 'personal' };
    state.currentFolderId = '';
    state.virtualView = null;
    state.pendingFocus = null;
    state.pendingFolderOps.clear();
    state.selectedItems.clear();
    list = document.createElement('div'); list.id = 'file-list'; list.tabIndex = 0;
    storage = document.createElement('div'); storage.id = 'storage-used';
    document.body.append(list, storage);
    app = mount(FileList, { target: list });
    flushSync();
});
afterEach(async () => {
    await unmount(app);
    list.remove(); storage.remove();
    state.pendingFolderOps.clear(); state.pendingFocus = null;
    state.selectedItems.clear(); state.userNames.clear();
    vi.restoreAllMocks();
});

it('merges root files without duplicating projected messages and shows plaintext sizes', async () => {
    api.getFolderContents.mockResolvedValue({
        folders: [{ id: 'folder', name: 'Work', parentId: '' }],
        files: [{ ...file('secret.txt'), encrypted: true, plaintextSize: 25 }],
    });
    deps.stats.mockResolvedValue(new Map([['folder', { id: 'folder', bytes: 4096, latestUpload: 200 }]]));
    api.getFileList.mockResolvedValue([
        { msgId: 41, name: 'duplicate.txt', size: 1000, date: 100, accessHash: 0 },
        { msgId: 99, name: 'raw.txt', size: 2000, date: 101, accessHash: 0 },
    ]);
    api.getAllFsMsgIds.mockResolvedValue([41]);
    state.pendingFolderOps.set('new', { parentId: '', name: 'Creating here' });
    state.pendingFolderOps.set('elsewhere', { parentId: 'other', name: 'Not here' });
    refreshFiles();
    await expectText('raw.txt');
    expect(list.textContent).toContain('Creating here');
    expect(list.textContent).not.toContain('Not here');
    expect(list.textContent).not.toContain('duplicate.txt');
    expect(list.querySelector('[data-name="secret.txt"]')?.textContent).toContain('25 B');
    expect(list.querySelector('[data-name="Work"]')?.textContent).toContain('4 KB');
    expect(state.telegramRootCacheDriveKey).toBe('1');
    expect(api.getAllFsMsgIds).not.toHaveBeenCalled();
});

it('keeps direct file de-duplication when folder stats fail without loading the full index', async () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {});
    api.getFolderContents.mockResolvedValue({ folders: [{ id: 'folder', name: 'Work', parentId: '' }], files: [file()] });
    api.getFileList.mockResolvedValue([{ msgId: 41, name: 'duplicate.txt', size: 1, date: 1, accessHash: 0 }]);
    api.getAllFsMsgIds.mockRejectedValue(new Error('index unavailable'));
    deps.stats.mockRejectedValue(new Error('stats unavailable'));
    refreshFiles();
    await expectText('report.txt');
    expect(list.querySelectorAll('[data-type="file"]')).toHaveLength(1);
    expect(list.textContent).toContain('Work');
    expect(api.getAllFsMsgIds).not.toHaveBeenCalled();
});

it('loads nested folders without fetching raw Telegram root files', async () => {
    state.currentFolderId = 'nested';
    refreshFiles();
    await expectText('report.txt');
    expect(api.getFolderContents).toHaveBeenCalledWith('nested');
    expect(api.getFileList).not.toHaveBeenCalled();
    expect(api.getAllFsMsgIds).not.toHaveBeenCalled();
});

it('rejects old drive results and old storage totals after navigation', async () => {
    const old = deferred<FolderContents>();
    const oldStorage = deferred<number>();
    api.getFolderContents.mockReturnValueOnce(old.promise);
    api.getStorageUsed.mockReturnValueOnce(oldStorage.promise);
    refreshFiles();
    state.activeChannel = { id: 2, title: 'Next', kind: 'personal' };
    api.getFolderContents.mockResolvedValue({ folders: [], files: [file('current.txt')] });
    refreshFiles();
    await expectText('current.txt');
    old.resolve({ folders: [], files: [file('stale.txt')] });
    oldStorage.resolve(999999);
    await old.promise;
    await oldStorage.promise;
    // Let pending promise continuations publish before inspecting the next frame.
    await new Promise<void>((resolve) => requestAnimationFrame(() => resolve()));
    flushSync();
    expect(list.textContent).toContain('current.txt');
    expect(list.textContent).not.toContain('stale.txt');
    expect(storage.innerText).toBe('1 KB / Unlimited');
    expect(state.telegramRootCacheDriveKey).toBe('2');
});

it.each(['photos', 'trash'] as const)('does not publish an in-flight drive load over %s', async (view) => {
    const old = deferred<FolderContents>();
    api.getFolderContents.mockReturnValue(old.promise);
    refreshFiles();
    state.virtualView = view;
    refreshFiles({ background: true });
    old.resolve({ folders: [], files: [file('stale.txt')] });
    await old.promise;
    await new Promise<void>((resolve) => requestAnimationFrame(() => resolve()));
    flushSync();
    expect(list.textContent).not.toContain('stale.txt');
    expect(view === 'photos' ? deps.gallery : deps.trash).toHaveBeenCalledTimes(1);
});

it('preserves visible rows and scroll on a failed same-view refresh', async () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {});
    refreshFiles(); await expectText('report.txt');
    list.scrollTop = 180;
    api.getFolderContents.mockRejectedValue(new Error('temporary failure'));
    refreshFiles();
    await vi.waitFor(() => expect(console.warn).toHaveBeenCalledWith('Same-view file refresh failed:', expect.any(Error)));
    expect(list.textContent).toContain('report.txt');
    expect(list.scrollTop).toBe(180);
    api.getFolderContents.mockResolvedValue({ folders: [], files: [file('updated.txt')] });
    refreshFiles(); await expectText('updated.txt');
    expect(list.scrollTop).toBe(180);
});

it.each([true, false])('shows an actionable navigation failure (offline=%s)', async (offline) => {
    deps.offline.mockReturnValue(offline);
    api.getFolderContents.mockRejectedValue(new Error('load failed'));
    refreshFiles();
    await expectText(offline ? "You're offline" : 'Could not load this folder');
    list.querySelector<HTMLButtonElement>('.file-state-actions button')?.click();
    expect(deps.refresh).toHaveBeenCalledTimes(1);
});

it('offers the upload picker and New folder in an empty desktop folder', async () => {
    api.getFolderContents.mockResolvedValue({ folders: [], files: [] });
    refreshFiles(); await expectText('This folder is empty');
    const buttons = Array.from(list.querySelectorAll<HTMLButtonElement>('.file-state-actions button'));
    expect(buttons.map((button) => button.textContent?.trim())).toEqual(['Upload files', 'New folder']);
    buttons[0].click();
    expect(deps.upload).toHaveBeenCalledTimes(1);
    buttons[1].click();
    expect(vi.mocked(openNewFolderModal)).toHaveBeenCalledTimes(1);
});

it('leaves a folder size blank until a real size is known', async () => {
    api.getFolderContents.mockResolvedValue({ folders: [{ id: 'empty', name: 'Empty', parentId: '' }], files: [] });
    deps.stats.mockResolvedValue(new Map());
    refreshFiles(); await expectText('Empty');
    expect(list.querySelector('.folder-row .folder-size')?.textContent).toBe('');
});

it('refreshes uploader labels only while the shared-drive request is current', async () => {
    const users = deferred<void>();
    deps.ensureUsers.mockReturnValue(users.promise);
    state.activeChannel = { id: 1, title: 'Shared', kind: 'shared' };
    refreshFiles(); await expectText('report.txt');
    state.userNames.set('7', 'Mara Okonkwo');
    deps.chip.mockReturnValue('Mara · now');
    users.resolve();
    await expectText('Mara · now');
    const view = get(fileListView);
    expect(view.kind === 'rows' && view.rows.find((row) => row.kind === 'file')?.uploaderChip)
        .toEqual({ label: 'Mara · now', firstName: 'Mara', initials: 'MO' });
});

it.each([NaN, -1])('does not display invalid storage usage %s', async (size) => {
    api.getStorageUsed.mockResolvedValue(size);
    refreshFiles(); await expectText('report.txt');
    expect(storage.innerText).toBe('— / Unlimited');
});


it.each([
    [7, 7, true], [8, 7, false], [0, 7, false], [7, 0, false],
])('enables shared-file mutations only for a known matching owner (%s/%s)', (uploaderId, selfId, allowed) => {
    state.activeChannel = { id: 1, title: 'Shared', kind: 'shared' };
    state.myUserID = selfId;
    const row = buildFileRow({ msgId: 41, name: 'report.txt', uploaderId }, 'nested');
    expect(row.canDelete).toBe(allowed);
    expect(row.canRename).toBe(allowed);
    expect(row.parentId).toBe('nested');
    expect(canOwnerActOnFile(null)).toBe(false);
});

it.each([7, 8, 0, undefined])('uses forwarded root sender %s for shared-drive rename permission', async (uploaderId) => {
    state.activeChannel = { id: 1, title: 'Shared', kind: 'shared' };
    state.myUserID = 7;
    api.getFolderContents.mockResolvedValue({ folders: [], files: [] });
    api.getFileList.mockResolvedValue([{ msgId: 99, name: 'forwarded.pdf', size: 10, date: 1, accessHash: 0, uploaderId }]);
    refreshFiles();
    await expectText('forwarded.pdf');
    const view = get(fileListView);
    if (view.kind !== 'rows') throw new Error('Forwarded file list did not load');
    const row = view.rows.find((candidate) => candidate.kind === 'file' && candidate.id === '99');
    if (!row || row.kind !== 'file') throw new Error('Forwarded row missing');
    expect(row.source).toBe('tg');
    expect(row.canRename).toBe(uploaderId === 7);
});

it('focuses and selects a requested file after its row mounts', async () => {
    state.pendingFocus = { type: 'file', id: 41 };
    refreshFiles();
    await vi.waitFor(() => {
        flushSync();
        expect(state.pendingFocus).toBeNull();
        expect(state.selectedItems.has('file:41')).toBe(true);
        expect(document.activeElement?.getAttribute('data-row-key')).toBe('file:41');
    });
});

it('retains pending focus until the requested file is available', async () => {
    state.pendingFocus = { type: 'file', id: 99 };
    refreshFiles(); await expectText('report.txt');
    expect(state.pendingFocus).toEqual({ type: 'file', id: 99 });
    expect(state.selectedItems.size).toBe(0);
});

it('does not apply late user names to a replacement personal-drive listing', async () => {
    const users = deferred<void>();
    deps.ensureUsers.mockReturnValue(users.promise);
    state.activeChannel = { id: 1, title: 'Shared', kind: 'shared' };
    refreshFiles(); await expectText('report.txt');
    state.activeChannel = { id: 2, title: 'Personal', kind: 'personal' };
    api.getFolderContents.mockResolvedValue({ folders: [], files: [file('mine.txt')] });
    refreshFiles(); await expectText('mine.txt');
    deps.chip.mockReturnValue('Stale name');
    users.resolve();
    await users.promise;
    flushSync();
    expect(list.textContent).not.toContain('Stale name');
});

it('shows unavailable storage when the usage query fails', async () => {
    api.getStorageUsed.mockRejectedValue(new Error('usage unavailable'));
    refreshFiles(); await expectText('report.txt');
    expect(storage.innerText).toBe('— / Unlimited');
});
