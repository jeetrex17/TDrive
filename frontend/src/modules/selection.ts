// Selection handling module for TDrive frontend

import { state } from '../state';
import { openDeleteModal } from './modals/delete';
import { openMoveModal } from './modals/move';
import { setSelectionCount } from '../ui/selection/selection-bar-store';
import { setSelectedFileRowKeys } from '../ui/file-list/row-state-store';
import { fileListRowForElement } from '../ui/file-list/row-lookup';
import type { FileCommandItem, FileListFileRow, FolderListRow } from '../ui/file-list/types';

const SELECTABLE_ROW_SELECTOR = '.drive-row[data-type="folder"], .drive-row[data-type="file"]';
let selectionAnchorKey = '';

type LogicalFileListRow = FolderListRow | FileListFileRow;

function emitSelectionChange(): void {
    window.dispatchEvent(new Event('tdrive:selectionchange'));
}

/**
 * A row element's identity, and the only thing selection reads off the markup.
 * Every other field it needs belongs to the row itself and is fetched from the
 * store, so the element and the row can never drift apart.
 */
export function getRowKey(row: HTMLElement): string {
    return row.dataset.rowKey ?? '';
}

function syncSelectedRowKeys(): void {
    setSelectedFileRowKeys(state.selectedItems.keys());
}

export function isRowSelected(row: HTMLElement): boolean {
    const key = getRowKey(row);
    return Boolean(key && state.selectedItems.has(key));
}

/**
 * The selection entry for a rendered row, keyed back to the row the list drew
 * it from. Null when the element no longer belongs to the published list, in
 * which case there is nothing to select: the caller leaves the selection alone
 * rather than adding an entry built from a row the reader cannot see.
 */
function selectionItemForElement(element: HTMLElement): { key: string; item: FileCommandItem } | null {
    const row = fileListRowForElement(element);
    if (!row) return null;
    return { key: row.selectionKey, item: logicalRowToSelectionItem(row, element) };
}

function logicalRowToSelectionItem(row: LogicalFileListRow, element?: HTMLElement): FileCommandItem {
    const withElement = element ? { row: element } : {};
    if (row.kind === 'folder') {
        return {
            type: 'folder',
            id: row.id,
            name: row.name,
            parentId: row.parentId,
            canDelete: true,
            canRename: true,
            ...withElement,
        };
    }
    if (row.source === 'tg') {
        return {
            type: 'file',
            id: Number(row.id),
            name: row.name,
            size: row.size,
            source: 'tg',
            parentId: row.parentId,
            uploaderID: row.uploaderID,
            canDelete: row.canDelete,
            canRename: row.canRename,
            ...withElement,
        };
    }
    return {
        type: 'file',
        id: Number(row.id),
        name: row.name,
        size: row.size,
        source: 'fs',
        parentId: row.parentId,
        uploaderID: row.uploaderID,
        canDelete: row.canDelete,
        canRename: row.canRename,
        ...withElement,
    };
}

function renderedRowsByKey(list: HTMLElement): Map<string, HTMLElement> {
    return new Map(Array.from(list.querySelectorAll<HTMLElement>(SELECTABLE_ROW_SELECTOR))
        .map((row) => [getRowKey(row), row] as const)
        .filter(([key]) => Boolean(key)));
}
export function updateSelectionBar(): void {
    syncSelectedRowKeys();
    if (!state.selectionBarEl) {
        emitSelectionChange();
        return;
    }
    const count = state.selectedItems.size;
    setSelectionCount(count);
    if (count === 0) {
        state.selectionBarEl.style.display = 'none';
        emitSelectionChange();
        return;
    }

    state.selectionBarEl.style.display = 'flex';
    emitSelectionChange();
}

export function clearSelection({ keepAnchor = false }: { keepAnchor?: boolean } = {}): void {
    state.selectedItems.clear();
    if (!keepAnchor) {
        selectionAnchorKey = '';
        state.selectionAnchorIndex = -1;
    }
    updateSelectionBar();
}

export function selectRow(row: HTMLElement, rowIndex: number): void {
    const selected = selectionItemForElement(row);
    if (!selected) return;
    state.selectedItems.set(selected.key, selected.item);
    selectionAnchorKey = selected.key;
    state.selectionAnchorIndex = rowIndex;
    updateSelectionBar();
}

export function deselectRow(row: HTMLElement): void {
    const key = getRowKey(row);
    if (!key) return;
    state.selectedItems.delete(key);
    updateSelectionBar();
}

// A keyed file-list update can replace row nodes. When the grid is windowed,
// logicalRows keeps offscreen selections and the range anchor intact.
export function reconcileSelection(list: HTMLElement, logicalRows?: readonly LogicalFileListRow[]): void {
    const renderedRows = Array.from(list.querySelectorAll<HTMLElement>(SELECTABLE_ROW_SELECTOR));
    const renderedByKey = renderedRowsByKey(list);
    const previous = state.selectedItems;
    const next = new Map<string, FileCommandItem>();

    if (logicalRows) {
        for (const row of logicalRows) {
            if (!previous.has(row.selectionKey)) continue;
            next.set(row.selectionKey, logicalRowToSelectionItem(row, renderedByKey.get(row.selectionKey)));
        }
    } else {
        for (const row of renderedRows) {
            const selected = selectionItemForElement(row);
            if (!selected || !previous.has(selected.key)) continue;
            next.set(selected.key, selected.item);
        }
    }

    previous.clear();
    for (const [key, item] of next) previous.set(key, item);

    const anchorIndex = logicalRows
        ? logicalRows.findIndex((row) => row.selectionKey === selectionAnchorKey)
        : renderedRows.findIndex((row) => getRowKey(row) === selectionAnchorKey);
    if (anchorIndex === -1) {
        selectionAnchorKey = '';
        state.selectionAnchorIndex = -1;
    } else {
        state.selectionAnchorIndex = anchorIndex;
    }
    updateSelectionBar();
}

export function handleRowSelection(
    row: HTMLElement,
    event: MouseEvent | KeyboardEvent,
    logicalRows?: readonly LogicalFileListRow[],
): void {
    if ('button' in event && event.button === 2) return;

    const list = document.getElementById('file-list');
    const renderedRows = list ? Array.from(list.querySelectorAll<HTMLElement>(SELECTABLE_ROW_SELECTOR)) : [];
    const key = getRowKey(row);
    const index = logicalRows
        ? logicalRows.findIndex((candidate) => candidate.selectionKey === key)
        : renderedRows.indexOf(row);
    if (index === -1) return;

    const isToggle = event.metaKey || event.ctrlKey;
    const anchorIndex = selectionAnchorKey
        ? logicalRows
            ? logicalRows.findIndex((candidate) => candidate.selectionKey === selectionAnchorKey)
            : renderedRows.findIndex((candidate) => getRowKey(candidate) === selectionAnchorKey)
        : state.selectionAnchorIndex;
    const isRange = event.shiftKey && anchorIndex >= 0;

    if (isRange) {
        const start = Math.min(anchorIndex, index);
        const end = Math.max(anchorIndex, index);
        if (!isToggle) clearSelection({ keepAnchor: true });
        const renderedByKey = list ? renderedRowsByKey(list) : new Map<string, HTMLElement>();

        for (let cursor = start; cursor <= end; cursor += 1) {
            if (logicalRows) {
                const rangeRow = logicalRows[cursor];
                if (!rangeRow || state.selectedItems.has(rangeRow.selectionKey)) continue;
                state.selectedItems.set(
                    rangeRow.selectionKey,
                    logicalRowToSelectionItem(rangeRow, renderedByKey.get(rangeRow.selectionKey)),
                );
                continue;
            }
            const rangeRow = renderedRows[cursor];
            if (!rangeRow) continue;
            const selected = selectionItemForElement(rangeRow);
            if (!selected || state.selectedItems.has(selected.key)) continue;
            state.selectedItems.set(selected.key, selected.item);
        }
        updateSelectionBar();
        return;
    }

    if (isToggle) {
        if (isRowSelected(row)) deselectRow(row);
        else selectRow(row, index);
        return;
    }

    clearSelection({ keepAnchor: true });
    selectRow(row, index);
}

export function ensureRowSelectedForContextMenu(row: HTMLElement): void {
    const list = document.getElementById('file-list');
    const rows = list ? Array.from(list.querySelectorAll<HTMLElement>(SELECTABLE_ROW_SELECTOR)) : [];
    const index = rows.indexOf(row);
    if (index === -1) return;

    if (!isRowSelected(row) || state.selectedItems.size === 0) {
        clearSelection({ keepAnchor: true });
        selectRow(row, index);
        return;
    }
    selectionAnchorKey = getRowKey(row);
    state.selectionAnchorIndex = index;
}

export function getSelectionPayload(): FileCommandItem[] {
    return Array.from(state.selectedItems.values(), (item): FileCommandItem => {
        if (item.type === 'folder') {
            return {
                type: 'folder',
                id: item.id,
                name: item.name,
                parentId: item.parentId,
                canDelete: item.canDelete,
                canRename: item.canRename,
            };
        }
        if (item.source === 'tg') {
            return {
                type: 'file',
                id: item.id,
                name: item.name,
                size: item.size,
                source: 'tg',
                parentId: item.parentId,
                uploaderID: item.uploaderID,
                canDelete: item.canDelete,
                canRename: item.canRename,
            };
        }
        return {
            type: 'file',
            id: item.id,
            name: item.name,
            size: item.size,
            source: 'fs',
            parentId: item.parentId,
            uploaderID: item.uploaderID,
            canDelete: item.canDelete,
            canRename: item.canRename,
        };
    });
}

export function openSelectedItemsDelete(): void {
    if (state.selectedItems.size === 0) return;
    openDeleteModal({ type: 'bulk', items: getSelectionPayload(), parentId: state.currentFolderId });
}

export function openSelectedItemsMove(): void {
    if (state.selectedItems.size === 0) return;
    openMoveModal({ type: 'bulk', items: getSelectionPayload(), parentId: state.currentFolderId });
}

export function activateSelectionBar(): () => void {
    const selectionBar = document.getElementById('selection-bar');
    if (!selectionBar) return () => {};

    state.selectionBarEl = selectionBar;
    const list = document.getElementById('file-list');
    const onListClick = (event: MouseEvent) => {
        if ((event.target as HTMLElement).closest('.drive-row')) return;
        clearSelection();
    };
    const onKeydown = (event: KeyboardEvent) => {
        if (event.key === 'Escape') clearSelection();
    };

    list?.addEventListener('click', onListClick);
    window.addEventListener('keydown', onKeydown);
    updateSelectionBar();

    return () => {
        list?.removeEventListener('click', onListClick);
        window.removeEventListener('keydown', onKeydown);
        clearSelection();
        if (state.selectionBarEl === selectionBar) state.selectionBarEl = null;
    };
}
