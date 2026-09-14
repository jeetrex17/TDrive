import { afterEach, describe, expect, it } from 'vitest';
import { state } from '../state';
import { clearSelection, handleRowSelection, reconcileSelection, selectRow } from './selection';
import type { FileListFileRow } from '../ui/file-list/types';

let list: HTMLElement | null = null;

function makeFileRow(id: number): HTMLElement {
    const row = document.createElement('div');
    row.className = 'drive-row';
    row.dataset.type = 'file';
    row.dataset.rowKey = `file:${id}`;
    row.dataset.id = String(id);
    row.dataset.name = `file-${id}.txt`;
    row.dataset.parentId = '';
    row.dataset.source = 'fs';
    row.dataset.size = '1';
    return row;
}

function makeLogicalFileRow(id: number): FileListFileRow {
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
    };
}

afterEach(() => {
    clearSelection();
    list?.remove();
    list = null;
});

describe('selection reconciliation', () => {
    it('retains selected identities across a published replacement and ranges from the reconciled anchor', () => {
        list = document.createElement('div');
        list.id = 'file-list';
        const first = makeFileRow(1);
        const second = makeFileRow(2);
        list.append(first, second);
        document.body.appendChild(list);
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
        list = document.createElement('div');
        list.id = 'file-list';
        const first = makeFileRow(1);
        const second = makeFileRow(2);
        list.append(first);
        document.body.appendChild(list);
        selectRow(first, 0);

        list.replaceChildren(second);
        const logicalRows = [makeLogicalFileRow(1), makeLogicalFileRow(2)];
        reconcileSelection(list, logicalRows);
        handleRowSelection(second, new KeyboardEvent('keydown', { shiftKey: true }), logicalRows);

        expect(state.selectedItems.get('file:1')?.row).toBeUndefined();
        expect([...state.selectedItems.keys()]).toEqual(['file:1', 'file:2']);
    });
});
