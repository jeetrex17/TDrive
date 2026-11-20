// The row menu is built from the row the list published, not from the markup
// that row was drawn into. These tests keep the two apart by giving the element
// nothing but its key.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { ContextMenuItem } from '../ui/menus/context-menu-store';

const menu = vi.hoisted(() => ({ showContextMenu: vi.fn(), hideContextMenu: vi.fn() }));
const transfers = vi.hoisted(() => ({
    enqueueDownload: vi.fn(),
    enqueueFolderDownload: vi.fn(),
    importFolderWithParentID: vi.fn(),
    uploadWithParentID: vi.fn(),
}));
const sourceCopies = vi.hoisted(() => ({ openSharedDriveCopy: vi.fn() }));
const selectedItems = vi.hoisted(() => vi.fn<() => FileCommandItem[]>(() => []));

vi.mock('../api', () => ({ isMobilePlatform: () => false }));
vi.mock('./transfers', () => transfers);
vi.mock('./source-copies', () => sourceCopies);
vi.mock('./selection', () => ({
    clearSelection: vi.fn(),
    ensureRowSelectedForContextMenu: vi.fn(),
    getSelectionPayload: selectedItems,
}));
vi.mock('./modals/delete', () => ({ moveToTrash: vi.fn() }));
vi.mock('./modals/rename', () => ({ openRenameModal: vi.fn() }));
vi.mock('./modals/move', () => ({ openMoveModal: vi.fn() }));
vi.mock('./modals/folder', () => ({ openNewFolderModal: vi.fn() }));
vi.mock('./navigation', () => ({ navigateToFolder: vi.fn() }));
vi.mock('./media-types', () => ({
    isVideoFile: () => false,
    canOpenFileViewer: () => false,
    isImageFile: (name: string) => name.endsWith('.png'),
}));
vi.mock('./app-actions', () => ({ appActions: () => ({ triggerRefresh: vi.fn() }) }));
vi.mock('../ui/menus/context-menu-store', () => menu);

import { activateContextMenu, showRowContextMenu } from './context-menu';
import { moveToTrash } from './modals/delete';
import { showFileListRows, showFileListState } from '../ui/file-list/file-list-store';
import { state } from '../state';
import type { FileCommandItem, FileListFileRow } from '../ui/file-list/types';

let list: HTMLElement | null = null;

function publishedFileRow(overrides: Partial<FileListFileRow> = {}): FileListFileRow {
    return {
        kind: 'file',
        key: 'file:fs:42',
        selectionKey: 'file:42',
        id: '42',
        name: 'notes.txt',
        baseName: 'notes',
        ext: 'TXT',
        source: 'fs',
        parentId: '',
        channelId: 5,
        size: 2048,
        metaLabel: 'Today',
        sizeLabel: '2 KB',
        ariaLabel: 'File: notes.txt',
        uploaderID: 0,
        uploadTime: 0,
        encrypted: false,
        canDelete: true,
        canRename: true,
        actions: [],
        ...overrides,
    };
}

function renderRow(key: string): HTMLElement {
    list = document.createElement('div');
    list.id = 'file-list';
    const row = document.createElement('div');
    row.className = 'drive-row';
    row.dataset.type = 'file';
    row.dataset.rowKey = key;
    list.append(row);
    document.body.append(list);
    return row;
}

function shownLabels(): string[] {
    const items = menu.showContextMenu.mock.calls[0]?.[2] as ContextMenuItem[] | undefined;
    return (items ?? [])
        .filter((item): item is Extract<ContextMenuItem, { label: string }> => item.type !== 'divider')
        .map((item) => item.label);
}

beforeEach(() => {
    vi.clearAllMocks();
    state.activeChannel = { id: 9, title: 'Another drive', kind: 'personal' };
    state.channels = [{ id: 5, title: 'Shared files', kind: 'shared', isActive: false, inviteLink: '' }];
    state.myUserID = 7;
    state.currentFolderId = '';
    selectedItems.mockReturnValue([]);
});

afterEach(() => {
    showFileListState({ stateKind: 'loading', title: 'Loading files' });
    list?.remove();
    list = null;
    state.channels = [];
    state.myUserID = 0;
    state.selectedItems.clear();
});

describe('the row context menu', () => {
    it('offers Delete because the published row allows it, with nothing in the markup to say so', () => {
        showFileListRows([publishedFileRow({ canDelete: true })]);

        showRowContextMenu(renderRow('file:42'), 10, 20);

        expect(shownLabels()).toContain('Delete');
    });

    it('withholds Rename and Delete when the published row forbids them', () => {
        showFileListRows([publishedFileRow({ canDelete: false, canRename: false })]);

        showRowContextMenu(renderRow('file:42'), 10, 20);

        expect(shownLabels()).not.toContain('Delete');
        expect(shownLabels()).not.toContain('Rename…');
        expect(shownLabels()).toContain('Move to…');
    });

    it('downloads from the drive the row was rendered in, not the one now active', () => {
        showFileListRows([publishedFileRow({ channelId: 5 })]);

        showRowContextMenu(renderRow('file:42'), 10, 20);
        const download = (menu.showContextMenu.mock.calls[0]?.[2] as ContextMenuItem[])
            .find((item): item is Extract<ContextMenuItem, { label: string }> =>
                item.type !== 'divider' && item.label === 'Download');
        download?.action();

        expect(transfers.enqueueDownload).toHaveBeenCalledWith(42, 'notes.txt', 2048, 5);
    });

    it('keeps the row drive on a delayed Delete menu action', () => {
        showFileListRows([publishedFileRow({ channelId: 5 })]);
        showRowContextMenu(renderRow('file:42'), 10, 20);
        const deletion = (menu.showContextMenu.mock.calls[0]?.[2] as ContextMenuItem[])
            .find((item): item is Extract<ContextMenuItem, { label: string }> =>
                item.type !== 'divider' && item.label === 'Delete');
        deletion?.action();

        expect(moveToTrash).toHaveBeenCalledWith(expect.objectContaining({
            type: 'file', id: 42, channelId: 5,
        }));
    });

    it('copies a Shared Drive member’s file using its captured drive and revision', () => {
        showFileListRows([publishedFileRow({ channelId: 5, revision: 3, canDelete: false, canRename: false })]);
        showRowContextMenu(renderRow('file:42'), 10, 20);
        const copy = (menu.showContextMenu.mock.calls[0]?.[2] as ContextMenuItem[])
            .find((item): item is Extract<ContextMenuItem, { label: string }> =>
                item.type !== 'divider' && item.label === 'Copy to My Drive…');
        expect(copy).toBeDefined();
        copy?.action();

        expect(sourceCopies.openSharedDriveCopy).toHaveBeenCalledWith(7, { id: 5, title: 'Shared files' }, [
            { id: 42, name: 'notes.txt', revision: 3, identity: 'managed' },
        ]);
    });

    it.each([
        { channelId: 9, revision: 2 },
        { channelId: 5, revision: 2, encrypted: true },
        { channelId: 5, revision: 0 },
        { channelId: 5, source: 'tg' } as const,
    ])('withholds shared copying where the row has no usable shared identity', (overrides) => {
        showFileListRows([publishedFileRow(overrides)]);
        showRowContextMenu(renderRow('file:42'), 10, 20);
        expect(shownLabels()).not.toContain('Copy to My Drive…');
    });

    it('keeps managed and forwarded identities in a bulk copy', () => {
        const notes = publishedFileRow({ id: '42', revision: 3 });
        const forward = publishedFileRow({
            id: '43', key: 'file:tg:43', selectionKey: 'file:43', name: 'forwarded.pdf',
            source: 'tg', copyIdentity: 'raw:forwarded-file',
        });
        showFileListRows([notes, forward]);
        const items: FileCommandItem[] = [
            { type: 'file', id: 42, name: notes.name, source: 'fs', channelId: 5 },
            { type: 'file', id: 43, name: forward.name, source: 'tg', size: 2048, channelId: 5, parentId: '' },
        ];
        selectedItems.mockReturnValue(items);
        state.selectedItems.set(notes.selectionKey, items[0]);
        state.selectedItems.set(forward.selectionKey, items[1]);
        const row = renderRow(notes.selectionKey);
        const dispose = activateContextMenu();
        try {
            row.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true }));
        } finally {
            dispose();
        }
        const copy = (menu.showContextMenu.mock.calls[0]?.[2] as ContextMenuItem[])
            .find((item): item is Extract<ContextMenuItem, { label: string }> =>
                item.type !== 'divider' && item.label === 'Copy 2 files to My Drive…');
        expect(copy).toBeDefined();
        copy?.action();

        expect(sourceCopies.openSharedDriveCopy).toHaveBeenCalledWith(7, { id: 5, title: 'Shared files' }, [
            { id: 42, name: 'notes.txt', revision: 3, identity: 'managed' },
            { id: 43, name: 'forwarded.pdf', revision: 0, identity: 'raw:forwarded-file' },
        ]);
    });

    it('opens nothing for a row the list has already dropped', () => {
        showFileListRows([publishedFileRow()]);
        const row = renderRow('file:42');
        showFileListRows([publishedFileRow({ key: 'file:fs:7', selectionKey: 'file:7', id: '7' })]);

        showRowContextMenu(row, 10, 20);

        expect(menu.showContextMenu).not.toHaveBeenCalled();
    });

    it('keeps folder-background actions out of a file menu', () => {
        showFileListRows([publishedFileRow()]);

        showRowContextMenu(renderRow('file:42'), 10, 20);

        const labels = shownLabels();
        expect(labels).not.toContain('Upload files');
        expect(labels).not.toContain('Upload folder');
        expect(labels).not.toContain('New folder');
        expect(labels).not.toContain('Refresh');
    });

    it('offers Open first for an image', () => {
        showFileListRows([publishedFileRow({
            key: 'file:fs:7', selectionKey: 'file:7', id: '7', name: 'pic.png', ext: 'PNG',
        })]);

        showRowContextMenu(renderRow('file:7'), 10, 20);

        expect(shownLabels()[0]).toBe('Open');
    });

    it('gives Delete a keyboard shortcut hint', () => {
        showFileListRows([publishedFileRow({ canDelete: true })]);

        showRowContextMenu(renderRow('file:42'), 10, 20);

        const items = menu.showContextMenu.mock.calls[0]?.[2] as ContextMenuItem[];
        const del = items.find((item): item is Extract<ContextMenuItem, { label: string }> =>
            item.type !== 'divider' && item.label === 'Delete');
        expect(del?.shortcut).toBeTruthy();
        expect(del?.icon).toBe('delete');
    });
});
