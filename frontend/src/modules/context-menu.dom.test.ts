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

vi.mock('../api', () => ({ isMobilePlatform: () => false }));
vi.mock('./transfers', () => transfers);
vi.mock('./selection', () => ({
    clearSelection: vi.fn(),
    ensureRowSelectedForContextMenu: vi.fn(),
    getSelectionPayload: vi.fn(() => []),
}));
vi.mock('./modals/delete', () => ({ openDeleteModal: vi.fn() }));
vi.mock('./modals/rename', () => ({ openRenameModal: vi.fn() }));
vi.mock('./modals/move', () => ({ openMoveModal: vi.fn() }));
vi.mock('./modals/folder', () => ({ openNewFolderModal: vi.fn() }));
vi.mock('./navigation', () => ({ navigateToFolder: vi.fn() }));
vi.mock('./media-types', () => ({ isVideoFile: () => false, canOpenFileViewer: () => false }));
vi.mock('./app-actions', () => ({ appActions: () => ({ triggerRefresh: vi.fn() }) }));
vi.mock('../ui/menus/context-menu-store', () => menu);

import { showRowContextMenu } from './context-menu';
import { showFileListRows, showFileListState } from '../ui/file-list/file-list-store';
import { state } from '../state';
import type { FileListFileRow } from '../ui/file-list/types';

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
    state.currentFolderId = '';
});

afterEach(() => {
    showFileListState({ stateKind: 'loading', title: 'Loading files' });
    list?.remove();
    list = null;
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

    it('opens nothing for a row the list has already dropped', () => {
        showFileListRows([publishedFileRow()]);
        const row = renderRow('file:42');
        showFileListRows([publishedFileRow({ key: 'file:fs:7', selectionKey: 'file:7', id: '7' })]);

        showRowContextMenu(row, 10, 20);

        expect(menu.showContextMenu).not.toHaveBeenCalled();
    });
});
