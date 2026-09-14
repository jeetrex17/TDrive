// Selection handling module for TDrive frontend

import { state } from '../state';
import { openDeleteModal } from './modals/delete';
import { openMoveModal } from './modals/move';
import SelectionBar from '../ui/selection/SelectionBar.svelte';
import { setSelectionCount } from '../ui/selection/selection-bar-store';
import { setSelectedFileRowKeys } from '../ui/file-list/row-state-store';
import type { FileCommandItem, FileListFileRow, FileSource, FolderListRow } from '../ui/file-list/types';
import { mountSvelte } from '../ui';

const SELECTABLE_ROW_SELECTOR = '.drive-row[data-type="folder"], .drive-row[data-type="file"]';
let selectionBarMounted = false;
let selectionAnchorKey = '';

type LogicalFileListRow = FolderListRow | FileListFileRow;

function emitSelectionChange(): void {
    window.dispatchEvent(new Event('tdrive:selectionchange'));
}

export function getRowKey(row: HTMLElement): string {
    const explicitKey = row.dataset.rowKey ?? '';
    if (explicitKey) return explicitKey;
    const type = row.dataset.type ?? '';
    const id = row.dataset.id ?? '';
    return type && id ? `${type}:${id}` : '';
}

function syncSelectedRowKeys(): void {
    setSelectedFileRowKeys(state.selectedItems.keys());
}

export function isRowSelected(row: HTMLElement): boolean {
    const key = getRowKey(row);
    return Boolean(key && state.selectedItems.has(key));
}

export function rowToSelectionItem(row: HTMLElement): FileCommandItem {
    if (row.dataset.type === 'folder') {
        return {
            type: 'folder',
            id: row.dataset.id ?? '',
            name: row.dataset.name || 'Folder',
            parentId: row.dataset.parentId ?? '',
            canDelete: row.dataset.canDelete !== 'false',
            canRename: row.dataset.canRename !== 'false',
            row,
        };
    }

    const source: FileSource = row.dataset.source === 'tg' ? 'tg' : 'fs';
    return {
        type: 'file',
        id: Number(row.dataset.id ?? 0),
        name: row.dataset.name || 'File',
        size: Number(row.dataset.size ?? 0),
        source,
        parentId: row.dataset.parentId ?? '',
        uploaderID: Number(row.dataset.uploaderId ?? 0),
        canDelete: row.dataset.canDelete !== 'false',
        canRename: row.dataset.canRename !== 'false',
        row,
    };
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
    const key = getRowKey(row);
    if (!key) return;
    state.selectedItems.set(key, rowToSelectionItem(row));
    selectionAnchorKey = key;
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
            const key = getRowKey(row);
            if (!key || !previous.has(key)) continue;
            next.set(key, rowToSelectionItem(row));
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
            const rangeKey = getRowKey(rangeRow);
            if (!rangeKey || state.selectedItems.has(rangeKey)) continue;
            state.selectedItems.set(rangeKey, rowToSelectionItem(rangeRow));
        }
        updateSelectionBar();
        return;
    }

    if (isToggle) {
        if (isRowSelected(row)) {
            deselectRow(row);
        } else {
            if (!key) return;
            state.selectedItems.set(key, rowToSelectionItem(row));
            selectionAnchorKey = key;
            state.selectionAnchorIndex = index;
            updateSelectionBar();
        }
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

export function setupSelectionBar(): void {
    state.selectionBarEl = document.getElementById('selection-bar');
    if (!state.selectionBarEl) return;

    if (!selectionBarMounted) {
        state.selectionBarEl.replaceChildren();
        mountSvelte(SelectionBar, {
            target: state.selectionBarEl,
            props: {
                onClear: () => clearSelection(),
                onDelete: () => {
                    if (state.selectedItems.size === 0) return;
                    openDeleteModal({ type: 'bulk', items: getSelectionPayload(), parentId: state.currentFolderId });
                },
                onMove: () => {
                    if (state.selectedItems.size === 0) return;
                    openMoveModal({ type: 'bulk', items: getSelectionPayload(), parentId: state.currentFolderId });
                },
            },
        });
        selectionBarMounted = true;
    }

    const list = document.getElementById('file-list');
    list?.addEventListener('click', (event) => {
        if ((event.target as HTMLElement).closest('.drive-row')) return;
        clearSelection();
    });

    window.addEventListener('keydown', (event) => {
        if (event.key === 'Escape') clearSelection();
    });

    updateSelectionBar();
}
