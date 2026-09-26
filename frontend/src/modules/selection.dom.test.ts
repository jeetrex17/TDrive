import { afterEach, describe, expect, it } from 'vitest';
import { state } from '../state';
import { clearSelection, handleRowSelection, reconcileSelection, selectRow } from './selection';
import { showFileListRows, showFileListState } from '../ui/file-list/file-list-store';
import type { FileListFileRow } from '../ui/file-list/types';

let list: HTMLElement | null = null;

// A row element carries its key and its kind; everything else about the row is
// looked up from the published list, which is what the app does too.
function makeFileRow(id: number): HTMLElement {
    const row = document.createElement('div');
    row.className = 'drive-row';
    row.dataset.type = 'file';
    row.dataset.rowKey = `file:${id}`;
    return row;
}

function makeLogicalFileRow(id: number, overrides: Partial<FileListFileRow> = {}): FileListFileRow {
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

function openList(...rows: HTMLElement[]): HTMLElement {
    const element = document.createElement('div');
    element.id = 'file-list';
    element.append(...rows);
    document.body.appendChild(element);
    return element;
}

afterEach(() => {
    clearSelection();
    showFileListState({ stateKind: 'loading', title: 'Loading files' });
    list?.remove();
    list = null;
});

describe('selection reconciliation', () => {
    it('retains selected identities across a published replacement and ranges from the reconciled anchor', () => {
        showFileListRows([makeLogicalFileRow(1), makeLogicalFileRow(2)]);
        const first = makeFileRow(1);
        const second = makeFileRow(2);
        list = openList(first, second);
        selectRow(first, 0);

        const replacementFirst = makeFileRow(1);
        const replacementSecond = makeFileRow(2);
        list.replaceChildren(replacementFirst, replacementSecond);
        reconcileSelection(list);

        expect(state.selectedItems.get('file:1')?.row).toBe(replacementFirst);
        expect(state.selectionAnchorIndex).toBe(0);

        handleRowSelection(replacementSecond, new KeyboardEvent('keydown', { shiftKey: true }));

        expect([...state.selectedItems.keys()]).toEqual(['file:1', 'file:2']);
    });

    it('keeps an offscreen anchor and selects its logical range', () => {
        const logicalRows = [makeLogicalFileRow(1), makeLogicalFileRow(2)];
        showFileListRows(logicalRows);
        const first = makeFileRow(1);
        const second = makeFileRow(2);
        list = openList(first);
        selectRow(first, 0);

        list.replaceChildren(second);
        reconcileSelection(list, logicalRows);
        handleRowSelection(second, new KeyboardEvent('keydown', { shiftKey: true }), logicalRows);

        expect(state.selectedItems.get('file:1')?.row).toBeUndefined();
        expect([...state.selectedItems.keys()]).toEqual(['file:1', 'file:2']);
    });

    it('takes the selected item from the published row rather than the markup', () => {
        showFileListRows([makeLogicalFileRow(1, { name: 'renamed.txt', size: 4096, source: 'tg' })]);
        const row = makeFileRow(1);
        list = openList(row);

        selectRow(row, 0);

        expect(state.selectedItems.get('file:1')).toMatchObject({
            type: 'file',
            id: 1,
            name: 'renamed.txt',
            size: 4096,
            source: 'tg',
        });
    });

    it('leaves the selection alone for a row the list has already dropped', () => {
        // The folder changed under an open gesture. Selecting from the stale
        // element would add an entry the reader cannot see, and one the next
        // reconcile would have to guess at.
        showFileListRows([makeLogicalFileRow(1)]);
        const row = makeFileRow(1);
        list = openList(row);
        showFileListRows([makeLogicalFileRow(2)]);

        selectRow(row, 0);

        expect(state.selectedItems.size).toBe(0);
    });

    it('will not resolve a detached row onto whichever row now holds its key', () => {
        // Same key, different drive: `file:1` is a different file after the
        // switch, so a torn-out element must not stand in for it.
        showFileListRows([makeLogicalFileRow(1, { name: 'old-drive.txt' })]);
        const row = makeFileRow(1);
        list = openList(row);
        row.remove();
        showFileListRows([makeLogicalFileRow(1, { name: 'new-drive.txt' })]);

        selectRow(row, 0);

        expect(state.selectedItems.size).toBe(0);
    });
});
