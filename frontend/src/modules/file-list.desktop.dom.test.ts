// The desktop list's keyboard and drag contract. Every one of these paths only
// ever holds an element, so each is a chance for the row it acts on to differ
// from the row the reader is looking at.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';

const api = vi.hoisted(() => ({
    isMobilePlatform: vi.fn(() => false),
    isIOSPlatform: () => false,
    isAndroidPlatform: () => false,
    onRuntimeEvent: () => () => {},
    playHaptic: vi.fn(),
}));
const dragDrop = vi.hoisted(() => ({
    beginRowDrag: vi.fn(),
    endRowDrag: vi.fn(),
    canDropOnFolder: vi.fn(() => true),
    setDropHighlight: vi.fn(),
    performDropMove: vi.fn(),
}));
const modals = vi.hoisted(() => ({
    openDeleteModal: vi.fn(),
    openRenameModal: vi.fn(),
    openMoveModal: vi.fn(),
    openNewFolderModal: vi.fn(),
}));

vi.mock('../api', () => api);
vi.mock('./drag-drop', () => dragDrop);
vi.mock('./modals/delete', () => ({ openDeleteModal: modals.openDeleteModal }));
vi.mock('./modals/rename', () => ({ openRenameModal: modals.openRenameModal }));
vi.mock('./modals/move', () => ({ openMoveModal: modals.openMoveModal }));
vi.mock('./modals/folder', () => ({ openNewFolderModal: modals.openNewFolderModal }));
vi.mock('./app-actions', () => ({ appActions: () => ({ playVideo: vi.fn(), openFile: vi.fn(), triggerRefresh: vi.fn(), refreshFiles: vi.fn() }) }));
vi.mock('./navigation', () => ({ navigateToFolder: vi.fn() }));
vi.mock('./transfers', () => ({
    chooseFilesForCurrentFolder: vi.fn(),
    enqueueDownload: vi.fn(),
    enqueueFolderDownload: vi.fn(),
}));
vi.mock('./connectivity', () => ({ isOffline: () => false }));
vi.mock('./context-menu', () => ({ showRowContextMenu: vi.fn() }));
vi.mock('./gallery', () => ({ renderGallery: vi.fn(), setPhotosMode: vi.fn() }));
vi.mock('./uploaders', () => ({ ensureUserNames: vi.fn(), uploaderChipLabel: () => null }));
vi.mock('./drive-data', () => ({ calculateVisibleFolderStats: vi.fn() }));
vi.mock('./folder-index', () => ({ refreshFolderIndex: vi.fn(() => Promise.resolve({ children: new Map() })), collectDescendants: vi.fn(() => []) }));

import FileList from '../ui/file-list/FileList.svelte';
import { activateFileList, buildFileRow, buildFolderRow, renderFileListRows } from './file-list';
import { state } from '../state';
import type { FileListFileRow } from '../ui/file-list/types';

let list: HTMLElement;
let app: Record<string, unknown> | null = null;
let deactivate = () => {};

function row(key: string): HTMLElement {
    const node = list.querySelector<HTMLElement>(`.drive-row[data-row-key="${key}"]`);
    if (!node) throw new Error(`missing row ${key}`);
    return node;
}

function press(target: Element, key: string): void {
    target.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true }));
}

function drag(target: Element, type: string): void {
    target.dispatchEvent(new Event(type, { bubbles: true, cancelable: true }));
}

function publish(...overrides: Partial<FileListFileRow>[]): void {
    renderFileListRows(list, [
        buildFolderRow({ id: 'design', name: 'Design' }, ''),
        buildFileRow({ id: 41, name: 'plan.pdf', size: 2_000_000, date: 1_700_000_000 }, '', overrides[0] ?? {}),
    ]);
    flushSync();
}

beforeEach(() => {
    vi.clearAllMocks();
    dragDrop.canDropOnFolder.mockReturnValue(true);
    state.activeChannel = { id: 1, title: 'Drive', kind: 'personal' };
    state.currentFolderId = '';
    state.selectedItems.clear();
    state.dragState = null;
    list = document.createElement('div');
    list.id = 'file-list';
    document.body.append(list);
    app = mount(FileList, { target: list });
    deactivate = activateFileList();
    publish();
});

afterEach(async () => {
    deactivate();
    state.selectedItems.clear();
    state.dragState = null;
    if (app) await unmount(app);
    app = null;
    list.remove();
});

describe('acting on a row from the keyboard', () => {
    it('deletes with the size and source the list published for the row', () => {
        press(row('file:41'), 'Delete');

        expect(modals.openDeleteModal).toHaveBeenCalledWith({
            type: 'file',
            id: 41,
            name: 'plan.pdf',
            size: 2_000_000,
            parentId: '',
            source: 'fs',
            canDelete: true,
        });
    });

    it('refuses to delete a row the published row says cannot be deleted', () => {
        publish({ canDelete: false });

        press(row('file:41'), 'Delete');

        expect(modals.openDeleteModal).not.toHaveBeenCalled();
    });

    it('refuses to rename a row the published row says cannot be renamed', () => {
        publish({ canRename: false });

        press(row('file:41'), 'F2');

        expect(modals.openRenameModal).not.toHaveBeenCalled();
    });

    it('renames a folder by its id rather than by a number parsed out of it', () => {
        press(row('folder:design'), 'F2');

        expect(modals.openRenameModal).toHaveBeenCalledWith({
            type: 'folder',
            id: 'design',
            name: 'Design',
            parentId: '',
        });
    });

    it('does nothing for a row the list does not publish', () => {
        // Left over from an earlier render: still in the markup, no longer a
        // row anybody can act on.
        const stale = document.createElement('div');
        stale.className = 'drive-row';
        stale.dataset.type = 'file';
        stale.dataset.rowKey = 'file:999';
        list.append(stale);

        press(stale, 'Delete');
        press(stale, 'F2');

        expect(modals.openDeleteModal).not.toHaveBeenCalled();
        expect(modals.openRenameModal).not.toHaveBeenCalled();
    });
});

describe('dragging a row', () => {
    it('drags the item the published row describes', () => {
        drag(row('file:41').querySelector('.row-name')!, 'dragstart');

        expect(dragDrop.beginRowDrag).toHaveBeenCalledWith(
            row('file:41'),
            [expect.objectContaining({ type: 'file', id: 41, name: 'plan.pdf', size: 2_000_000, source: 'fs', parentId: '' })],
            '',
            new Set(),
        );
    });

    it('offers a folder as a drop target by the id the list currently holds', () => {
        state.dragState = { items: [], parentId: '', blocked: new Set(), row: row('file:41') };

        drag(row('folder:design'), 'dragover');

        expect(dragDrop.canDropOnFolder).toHaveBeenCalledWith('design');
        expect(dragDrop.setDropHighlight).toHaveBeenCalledWith(row('folder:design'), true);
    });

    it('will not drop onto a file row, whatever the pointer is over', () => {
        state.dragState = { items: [], parentId: '', blocked: new Set(), row: row('file:41') };

        drag(row('file:41'), 'dragover');

        expect(dragDrop.setDropHighlight).not.toHaveBeenCalled();
    });

    it('moves into the folder the drop landed on', async () => {
        state.dragState = { items: [], parentId: '', blocked: new Set(), row: row('file:41') };

        drag(row('folder:design'), 'drop');
        await Promise.resolve();

        expect(dragDrop.performDropMove).toHaveBeenCalledWith('design');
    });
});
