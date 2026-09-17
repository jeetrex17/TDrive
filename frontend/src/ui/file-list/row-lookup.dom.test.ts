import { afterEach, describe, expect, it } from 'vitest';
import { showFileListRows, showFileListState } from './file-list-store';
import { fileListRowForElement, fileListRowForKey } from './row-lookup';
import type { FileListFileRow, FileListRow, FolderListRow, PendingFolderListRow } from './types';

let list: HTMLElement | null = null;

function fileRow(id: number, overrides: Partial<FileListFileRow> = {}): FileListFileRow {
    return {
        kind: 'file',
        key: `file:fs:${id}`,
        selectionKey: `file:${id}`,
        id: String(id),
        name: `file-${id}.txt`,
        baseName: `file-${id}`,
        ext: 'TXT',
        source: 'fs',
        parentId: '',
        channelId: 7,
        size: 1,
        metaLabel: 'Today',
        sizeLabel: '1 B',
        ariaLabel: `File: file-${id}.txt`,
        uploaderID: 0,
        uploadTime: id,
        encrypted: false,
        canDelete: true,
        canRename: true,
        actions: [],
        ...overrides,
    };
}

function folderRow(id: string): FolderListRow {
    return {
        kind: 'folder',
        key: `folder:${id}`,
        selectionKey: `folder:${id}`,
        id,
        name: id,
        parentId: '',
        channelId: 7,
        size: 0,
        modifiedTime: 0,
        metaLabel: '—',
        sizeLabel: '…',
        ariaLabel: `Folder: ${id}`,
        actions: [],
    };
}

function pendingRow(tempId: string): PendingFolderListRow {
    return { kind: 'pending-folder', key: `pending-folder:${tempId}`, tempId, name: 'New folder' };
}

// The markup the real list draws, reduced to what the lookup reads: a row key
// and somewhere inside it for an event to have come from.
function render(...keys: string[]): HTMLElement[] {
    list = document.createElement('div');
    list.id = 'file-list';
    const rows = keys.map((key) => {
        const row = document.createElement('div');
        row.className = 'drive-row';
        row.dataset.rowKey = key;
        row.append(document.createElement('span'));
        list?.append(row);
        return row;
    });
    document.body.append(list);
    return rows;
}

afterEach(() => {
    showFileListState({ stateKind: 'loading', title: 'Loading files' });
    list?.remove();
    list = null;
});

describe('finding the row an element stands for', () => {
    it('answers with the published row, not a copy of it', () => {
        const published = fileRow(42);
        showFileListRows([published]);
        const [element] = render('file:42');

        expect(fileListRowForElement(element)).toBe(published);
    });

    it('resolves an event target from deep inside the row', () => {
        showFileListRows([fileRow(42)]);
        const [element] = render('file:42');

        expect(fileListRowForElement(element.firstElementChild)).toBe(fileListRowForKey('file:42'));
    });

    it('has nothing to offer for an element that is not a row', () => {
        showFileListRows([fileRow(42)]);
        render('file:42');

        expect(fileListRowForElement(list)).toBeNull();
    });

    it('has nothing to offer while the list is showing a state rather than rows', () => {
        const [element] = render('file:42');
        showFileListState({ stateKind: 'empty', title: 'This folder is empty' });

        expect(fileListRowForElement(element)).toBeNull();
    });

    it('skips a pending folder, which has no identity to act on yet', () => {
        const rows: FileListRow[] = [pendingRow('pending:1'), folderRow('design')];
        showFileListRows(rows);

        expect(fileListRowForKey('pending-folder:pending:1')).toBeNull();
        expect(fileListRowForKey('folder:design')).toBe(rows[1]);
    });
});

describe('rows the list has moved on from', () => {
    it('forgets a key once the folder that held it is left', () => {
        showFileListRows([fileRow(42)]);
        const [element] = render('file:42');
        showFileListRows([fileRow(43)]);

        expect(fileListRowForElement(element)).toBeNull();
    });

    it('refuses a row torn out of the list, whose key another row may now hold', () => {
        // The drive switched under an open menu. `file:42` is a real key in the
        // new drive and a different file entirely, so answering with it would
        // point the menu's delete at the wrong item.
        showFileListRows([fileRow(42, { name: 'old-drive.txt' })]);
        const [element] = render('file:42');
        element.remove();
        showFileListRows([fileRow(42, { name: 'new-drive.txt' })]);

        expect(fileListRowForElement(element)).toBeNull();
    });

    it('still answers for a row the virtual window has not mounted', () => {
        // Selection and the keyboard reach past what is on screen; only the
        // element lookup needs a live element.
        const offscreen = fileRow(900);
        showFileListRows([fileRow(42), offscreen]);
        render('file:42');

        expect(fileListRowForKey('file:900')).toBe(offscreen);
    });
});

describe('the cost of a lookup', () => {
    it('walks the rows once per published view, not once per call', () => {
        // A drag resolves a row on every pointer-move. Counting the walks is
        // the only way to tell an index apart from a scan that happens to give
        // the same answer.
        let walks = 0;
        const rows: FileListRow[] = [fileRow(1), fileRow(2), fileRow(3)];
        Object.defineProperty(rows, Symbol.iterator, {
            value: function* iterate(this: FileListRow[]) {
                walks += 1;
                yield* Array.prototype.slice.call(this) as FileListRow[];
            },
        });
        showFileListRows(rows);

        expect(fileListRowForKey('file:1')?.id).toBe('1');
        expect(fileListRowForKey('file:3')?.id).toBe('3');
        expect(fileListRowForKey('file:2')?.id).toBe('2');

        expect(walks).toBe(1);
    });
});
