import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
    search: vi.fn(),
    getFileList: vi.fn(),
    refreshFiles: vi.fn(),
    renderFileState: vi.fn(),
    renderFileListRows: vi.fn(),
    buildFolderRow: vi.fn(),
    buildFileRow: vi.fn(),
    resolveUploaderChipsForRows: vi.fn(),
    syncDriveRowTabStops: vi.fn(),
}));

vi.mock('../api', () => ({
    getFileList: mocks.getFileList,
    search: mocks.search,
}));
vi.mock('./file-list', () => ({
    buildFileRow: mocks.buildFileRow,
    buildFolderRow: mocks.buildFolderRow,
    renderFileListRows: mocks.renderFileListRows,
    renderFileState: mocks.renderFileState,
    resetFileListScrollRestore: vi.fn(),
    resolveUploaderChipsForRows: mocks.resolveUploaderChipsForRows,
    syncDriveRowTabStops: mocks.syncDriveRowTabStops,
}));
vi.mock('./selection', () => ({
    clearSelection: vi.fn(),
    handleRowSelection: vi.fn(),
}));
vi.mock('./navigation', () => ({ renderBreadcrumb: vi.fn() }));
vi.mock('./gallery', () => ({ setPhotosMode: vi.fn() }));
vi.mock('./transfers', () => ({
    enqueueDownload: vi.fn(),
    enqueueFolderDownload: vi.fn(),
}));
vi.mock('./media-types', () => ({
    canOpenFileViewer: vi.fn(() => false),
    isVideoFile: vi.fn(() => false),
}));
vi.mock('./app-actions', () => ({
    appActions: () => ({
        refreshFiles: mocks.refreshFiles,
        triggerRefresh: vi.fn(),
        openFile: vi.fn(),
        playVideo: vi.fn(),
    }),
}));

import { state } from '../state';
import { clearSearch, runGlobalSearch, setupSearchBar } from './search';

function deferred<T>() {
    let resolve!: (value: T) => void;
    const promise = new Promise<T>((resolvePromise) => {
        resolve = resolvePromise;
    });
    return { promise, resolve };
}

function resetSearchDom() {
    document.body.innerHTML = '<input id="search-input"><div class="file-table-header"><span class="col-date">Uploaded</span></div><div id="file-list"></div>';
    state.activeChannel = { id: 1, title: 'Personal', kind: 'personal' };
    state.searchQuery = '';
    state.telegramRootCache = null;
    state.telegramRootCacheDriveKey = null;
    mocks.search.mockReset();
    mocks.search.mockResolvedValue([]);
    mocks.getFileList.mockReset();
    mocks.getFileList.mockResolvedValue([]);
    mocks.refreshFiles.mockReset();
    mocks.renderFileState.mockReset();
    mocks.renderFileListRows.mockReset();
    mocks.buildFolderRow.mockImplementation((_folder: unknown, _parent: string, overrides: Record<string, unknown>) => overrides);
    mocks.buildFileRow.mockImplementation((_file: unknown, _parent: string, overrides: Record<string, unknown>) => overrides);
    vi.useFakeTimers();
}

describe('search scheduling', () => {
    beforeEach(resetSearchDom);
    afterEach(() => {
        clearSearch({ refresh: false });
        vi.useRealTimers();
    });

    it('does not run a second search when Enter fires before the debounce', async () => {
        setupSearchBar();
        const input = document.getElementById('search-input') as HTMLInputElement;
        input.value = 'report';
        input.dispatchEvent(new Event('input', { bubbles: true }));
        input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true }));

        await vi.runAllTimersAsync();
        expect(mocks.search).toHaveBeenCalledTimes(1);
    });

    it('cancels pending debounce work when the search is cleared', async () => {
        setupSearchBar();
        const input = document.getElementById('search-input') as HTMLInputElement;
        input.value = 'stale';
        input.dispatchEvent(new Event('input', { bubbles: true }));
        input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }));

        await vi.runAllTimersAsync();
        expect(mocks.search).not.toHaveBeenCalled();
        expect(mocks.refreshFiles).toHaveBeenCalledTimes(1);
    });

    it('coalesces equivalent in-flight queries only within the same active drive', async () => {
        const firstSearch = deferred<[]>();
        const secondSearch = deferred<[]>();
        let calls = 0;
        mocks.search.mockImplementation(() => {
            calls += 1;
            return calls === 1 ? firstSearch.promise : secondSearch.promise;
        });

        state.searchQuery = ' report ';
        const first = runGlobalSearch();
        state.searchQuery = 'report';
        const equivalent = runGlobalSearch();
        expect(mocks.search).toHaveBeenCalledTimes(1);

        state.activeChannel = { id: 2, title: 'Shared', kind: 'shared' };
        const otherDrive = runGlobalSearch();
        expect(mocks.search).toHaveBeenCalledTimes(2);

        firstSearch.resolve([]);
        secondSearch.resolve([]);
        await Promise.all([first, equivalent, otherDrive]);
    });
});
