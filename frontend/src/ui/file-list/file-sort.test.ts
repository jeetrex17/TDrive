import { describe, expect, it } from 'vitest';
import { nextFileSortState, sortFileListRows, type FileSortState } from './file-sort';
import type { FileListFileRow, FolderListRow, PendingFolderListRow } from './types';

function file(overrides: Partial<FileListFileRow>): FileListFileRow {
    const name = overrides.name ?? 'file.txt';
    return {
        kind: 'file',
        key: `file:${overrides.id ?? name}`,
        selectionKey: `file:${overrides.id ?? name}`,
        id: String(overrides.id ?? name),
        name,
        baseName: name.replace(/\.[^.]+$/, ''),
        ext: 'TXT',
        source: 'fs',
        parentId: '',
        metaLabel: '',
        sizeLabel: '',
        ariaLabel: `File: ${name}`,
        size: 0,
        uploadTime: 0,
        uploaderID: 0,
        encrypted: false,
        canDelete: true,
        canRename: true,
        actions: [],
        ...overrides,
    };
}

function folder(id: string, name: string, size = 0, modifiedTime = 0): FolderListRow {
    return {
        kind: 'folder',
        key: `folder:${id}`,
        selectionKey: `folder:${id}`,
        id,
        name,
        parentId: '',
        metaLabel: '—',
        sizeLabel: '…',
        size,
        modifiedTime,
        ariaLabel: `Folder: ${name}`,
        actions: [],
    };
}

function pending(tempId: string, name: string): PendingFolderListRow {
    return { kind: 'pending-folder', key: `pending:${tempId}`, tempId, name };
}

function keysFor(state: FileSortState) {
    return sortFileListRows([
        file({ id: 'small-old', name: 'beta.mp4', size: 10, uploadTime: 100 }),
        folder('z', 'Zoo'),
        file({ id: 'large-new', name: 'alpha.mp4', size: 90, uploadTime: 300 }),
        pending('1', 'Creating'),
        folder('a', 'Archive'),
        file({ id: 'mid', name: 'gamma.mp4', size: 50, uploadTime: 200 }),
    ], state).map((row) => row.key);
}

describe('file list sorting', () => {
    it('keeps pending rows first and folders pinned above files', () => {
        expect(keysFor({ key: 'date', direction: 'desc' })).toEqual([
            'pending:1',
            'folder:a',
            'folder:z',
            'file:large-new',
            'file:mid',
            'file:small-old',
        ]);
    });

    it('sorts files by name in either direction', () => {
        expect(keysFor({ key: 'name', direction: 'asc' }).slice(3)).toEqual([
            'file:large-new',
            'file:small-old',
            'file:mid',
        ]);
        expect(keysFor({ key: 'name', direction: 'desc' }).slice(3)).toEqual([
            'file:mid',
            'file:small-old',
            'file:large-new',
        ]);
    });

    it('sorts files by size using deterministic name tie-breaks', () => {
        const rows = sortFileListRows([
            file({ id: 'a', name: 'alpha.mp4', size: 10, uploadTime: 1 }),
            file({ id: 'b', name: 'beta.mp4', size: 10, uploadTime: 2 }),
            file({ id: 'c', name: 'charlie.mp4', size: 20, uploadTime: 3 }),
        ], { key: 'size', direction: 'desc' });

        expect(rows.map((row) => row.key)).toEqual(['file:c', 'file:a', 'file:b']);
    });

    it('sorts folders by size in either direction with name tie-breaks', () => {
        const rows = [
            folder('small', 'Small', 10),
            folder('big', 'Big', 90),
            folder('mid-b', 'Beta', 50),
            folder('mid-a', 'Alpha', 50),
            file({ id: 'f', name: 'file.mp4', size: 1, uploadTime: 1 }),
        ];
        const keys = (state: FileSortState) => sortFileListRows(rows, state).map((row) => row.key);

        expect(keys({ key: 'size', direction: 'desc' })).toEqual([
            'folder:big',
            'folder:mid-a',
            'folder:mid-b',
            'folder:small',
            'file:f',
        ]);
        expect(keys({ key: 'size', direction: 'asc' })).toEqual([
            'folder:small',
            'folder:mid-a',
            'folder:mid-b',
            'folder:big',
            'file:f',
        ]);
    });

    it('sorts folders by latest upload under the date column in either direction', () => {
        // Names run opposite to dates so a date sort that fell back to name
        // would reorder these; a folder with no uploads sorts as oldest.
        const rows = [
            folder('old', 'Zed', 0, 100),
            folder('new', 'Alpha', 0, 300),
            folder('none', 'Mid', 0, 0),
        ];
        const keys = (state: FileSortState) => sortFileListRows(rows, state).map((row) => row.key);

        expect(keys({ key: 'date', direction: 'desc' })).toEqual(['folder:new', 'folder:old', 'folder:none']);
        expect(keys({ key: 'date', direction: 'asc' })).toEqual(['folder:none', 'folder:old', 'folder:new']);
        expect(keys({ key: 'name', direction: 'desc' })).toEqual(['folder:old', 'folder:none', 'folder:new']);
    });

    it('toggles an active column and applies sensible defaults for new columns', () => {
        expect(nextFileSortState({ key: 'date', direction: 'desc' }, 'date')).toEqual({ key: 'date', direction: 'asc' });
        expect(nextFileSortState({ key: 'date', direction: 'asc' }, 'name')).toEqual({ key: 'name', direction: 'asc' });
        expect(nextFileSortState({ key: 'name', direction: 'asc' }, 'size')).toEqual({ key: 'size', direction: 'desc' });
    });
});
