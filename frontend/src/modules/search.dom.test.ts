import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
    search: vi.fn(),
    getFileList: vi.fn(),
    getAllFsMsgIds: vi.fn(async () => [] as number[]),
    refreshFiles: vi.fn(),
    renderFileState: vi.fn(),
    renderFileListRows: vi.fn(),
    buildFolderRow: vi.fn(),
    buildFileRow: vi.fn(),
    resolveUploaderChipsForRows: vi.fn(),
    syncDriveRowTabStops: vi.fn(),
    deselectRow: vi.fn(),
    handleRowSelection: vi.fn(),
    isRowSelected: vi.fn(() => false),
    selectRow: vi.fn(),
    enqueueDownload: vi.fn(),
    enqueueFolderDownload: vi.fn(),
    isMobilePlatform: vi.fn(() => true),
}));

vi.mock('../api', () => ({
    getAllFsMsgIds: mocks.getAllFsMsgIds,
    getFileList: mocks.getFileList,
    search: mocks.search,
    isMobilePlatform: mocks.isMobilePlatform,
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
    handleRowSelection: mocks.handleRowSelection,
    deselectRow: mocks.deselectRow,
    isRowSelected: mocks.isRowSelected,
    selectRow: mocks.selectRow,
}));
vi.mock('./navigation', () => ({ renderBreadcrumb: vi.fn() }));
vi.mock('./gallery', () => ({ setPhotosMode: vi.fn() }));
vi.mock('./transfers', () => ({
    enqueueDownload: mocks.enqueueDownload,
    enqueueFolderDownload: mocks.enqueueFolderDownload,
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
import { activateSearchBar, clearSearch, runGlobalSearch } from './search';
import { showFileListRows, showFileListState } from '../ui/file-list/file-list-store';

function deferred<T>() {
    let resolve!: (value: T) => void;
    const promise = new Promise<T>((resolvePromise) => {
        resolve = resolvePromise;
    });
    return { promise, resolve };
}

let deactivateSearchBar = () => {};

function resetSearchDom() { document.body.innerHTML = '<input id="search-input"><div class="file-table-header"><span class="col-date">Uploaded</span></div><div id="file-list"></div>';
state.activeChannel = { id: 1, title: 'Personal', kind: 'personal' };
state.searchQuery = '';
state.selectedItems.clear();
state.telegramRootCache = null;
state.telegramRootCacheDriveKey = null;
mocks.search.mockReset();
mocks.search.mockResolvedValue([]);
mocks.getFileList.mockReset();
mocks.getFileList.mockResolvedValue([]);
mocks.getAllFsMsgIds.mockReset();
mocks.getAllFsMsgIds.mockResolvedValue([]);
mocks.refreshFiles.mockReset();
mocks.deselectRow.mockReset();
mocks.isRowSelected.mockReset();
mocks.isRowSelected.mockReturnValue(false);
mocks.selectRow.mockReset();
mocks.enqueueDownload.mockReset();
mocks.enqueueFolderDownload.mockReset();
mocks.handleRowSelection.mockReset();
mocks.isMobilePlatform.mockReturnValue(true);
mocks.renderFileState.mockReset();
mocks.renderFileListRows.mockReset();
mocks.renderFileListRows.mockImplementation((_list: HTMLElement, rows: unknown[]) => showFileListRows(rows as never[]));
mocks.buildFolderRow.mockImplementation((folder: { id?: string }, _parent: string, overrides: Record<string, unknown>) => ({
    kind: 'folder', selectionKey: `folder:${folder.id ?? ''}`, ...overrides,
}));
mocks.buildFileRow.mockImplementation((file: { id?: string | number }, _parent: string, overrides: Record<string, unknown>) => ({
    kind: 'file', selectionKey: `file:${file.id ?? ''}`, ...overrides,
}));
showFileListState({ stateKind: 'loading', title: 'Loading files' });
vi.useFakeTimers(); }

describe('search scheduling', () => {
    beforeEach(resetSearchDom);
    afterEach(() => {
        deactivateSearchBar();
                clearSearch({ refresh: false });
        vi.useRealTimers();
    });

    it('does not run a second search when Enter fires before the debounce', async () => {
        deactivateSearchBar = activateSearchBar();
        const input = document.getElementById('search-input') as HTMLInputElement;
        input.value = 'report';
        input.dispatchEvent(new Event('input', { bubbles: true }));
        input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true }));

        await vi.runAllTimersAsync();
        expect(mocks.search).toHaveBeenCalledTimes(1);
    });

    it('cancels pending debounce work when the search is cleared', async () => {
        deactivateSearchBar = activateSearchBar();
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

    it('keeps a search result download bound to the drive that produced the action', async () => {
        state.searchQuery = 'plan';
        mocks.search.mockResolvedValue([{
            type: 'file', id: 42, name: 'plan.bin', size: 10, parentId: '', path: 'Drive A', source: 'fs',
        }]);

        await runGlobalSearch();
        const row = mocks.buildFileRow.mock.results[0]?.value as { actions: Array<{ kind: string; onClick?: () => void }> };
        state.activeChannel = { id: 2, title: 'Drive B', kind: 'shared' };
        row.actions.find((action) => action.kind === 'download')?.onClick?.();

        expect(mocks.enqueueDownload).toHaveBeenCalledWith(42, 'plan.bin', 10, 1);
    });

    it('leaves a deleted file out of the results instead of offering the raw message', async () => {
        mocks.buildFileRow.mockClear();
        state.searchQuery = 'ghost';
        // Deleted: no hit for it any more, but its Telegram message is still
        // TDrive's until the trash purges it.
        mocks.getFileList.mockResolvedValue([{ msgId: 77, name: 'ghost.bin', size: 10, date: 1 }]);
        mocks.getAllFsMsgIds.mockResolvedValue([77]);

        await runGlobalSearch();

        expect(mocks.buildFileRow).not.toHaveBeenCalled();
    });

    it('still offers a file that was never TDrive\'s to begin with', async () => {
        mocks.buildFileRow.mockClear();
        state.searchQuery = 'ghost';
        mocks.getFileList.mockResolvedValue([{ msgId: 77, name: 'ghost.bin', size: 10, date: 1 }]);
        mocks.getAllFsMsgIds.mockResolvedValue([]);

        await runGlobalSearch();

        expect(mocks.buildFileRow).toHaveBeenCalledOnce();
    });

    it('opens a mobile search result with one tap instead of selecting it', async () => {
        state.searchQuery = 'report';
        mocks.search.mockResolvedValue([{
            type: 'folder',
            id: 'reports',
            name: 'Reports',
            parentId: '',
            path: 'My Drive',
        }]);

        await runGlobalSearch();
        const row = mocks.buildFolderRow.mock.results[0]?.value as { onClick?: (event: MouseEvent) => void };
        const element = document.createElement('div');
        element.className = 'drive-row';
        const label = document.createElement('span');
        element.append(label);
        const list = document.getElementById('file-list')!;
        const reachedList = vi.fn();
        list.addEventListener('click', reachedList);
        list.append(element);
        element.addEventListener('click', (event) => row.onClick?.(event));

        label.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));

        expect(state.currentFolderId).toBe('reports');
        expect(mocks.refreshFiles).toHaveBeenCalledTimes(1);
        expect(reachedList).not.toHaveBeenCalled();
    });

    it('leaves a mobile folder result action button to the shared row action handler', async () => {
        state.searchQuery = 'report';
        mocks.search.mockResolvedValue([{
            type: 'folder',
            id: 'reports',
            name: 'Reports',
            parentId: '',
            path: 'My Drive',
        }]);

        await runGlobalSearch();
        const row = mocks.buildFolderRow.mock.results[0]?.value as { onClick?: (event: MouseEvent) => void };
        const element = document.createElement('div');
        element.className = 'drive-row';
        const more = document.createElement('button');
        more.className = 'row-more';
        element.append(more);
        document.getElementById('file-list')!.append(element);
        element.addEventListener('click', (event) => row.onClick?.(event));

        more.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));

        expect(mocks.refreshFiles).not.toHaveBeenCalled();
        expect(state.searchQuery).toBe('report');
    });

    it('keeps a mobile search tap in explicit selection mode once selection exists', async () => {
        state.searchQuery = 'report';
        mocks.search.mockResolvedValue([{
            type: 'folder',
            id: 'reports',
            name: 'Reports',
            parentId: '',
            path: 'My Drive',
        }]);

        await runGlobalSearch();
        state.selectedItems.set('file:existing', { type: 'file', id: 1, name: 'existing', size: 0, source: 'fs', parentId: '' });
        const row = mocks.buildFolderRow.mock.results[0]?.value as { onClick?: (event: MouseEvent) => void };
        const element = document.createElement('div');
        element.className = 'drive-row';
        element.dataset.rowKey = 'folder:reports';
        const label = document.createElement('span');
        element.append(label);
        document.getElementById('file-list')!.append(element);
        element.addEventListener('click', (event) => row.onClick?.(event));

        label.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));

        expect(mocks.selectRow).toHaveBeenCalledWith(element, expect.any(Number));
        expect(mocks.refreshFiles).not.toHaveBeenCalled();
    });

    it('passes logical search rows to desktop selection for virtual range selection', async () => {
        mocks.isMobilePlatform.mockReturnValue(false);
        state.searchQuery = 'report';
        mocks.search.mockResolvedValue([{
            type: 'folder',
            id: 'reports',
            name: 'Reports',
            parentId: '',
            path: 'My Drive',
        }]);

        await runGlobalSearch();
        const row = mocks.buildFolderRow.mock.results[0]?.value as { onClick?: (event: MouseEvent) => void };
        const element = document.createElement('div');
        element.className = 'drive-row';
        element.dataset.rowKey = 'folder:reports';
        const label = document.createElement('span');
        element.append(label);
        document.getElementById('file-list')!.append(element);
        element.addEventListener('click', (event) => row.onClick?.(event));

        label.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));

        expect(mocks.handleRowSelection).toHaveBeenCalledWith(
            element,
            expect.any(MouseEvent),
            [expect.objectContaining({ selectionKey: 'folder:reports' })],
        );
    });
});
