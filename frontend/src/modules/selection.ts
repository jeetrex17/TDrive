// Selection handling module for TDrive frontend

import { state } from '../state';
import { openDeleteModal } from './modals/delete';
import { openMoveModal } from './modals/move';
import SelectionBar from '../ui/selection/SelectionBar.svelte';
import { setSelectionCount } from '../ui/selection/selection-bar-store';
import { setSelectedFileRowKeys } from '../ui/file-list/row-state-store';
import { mountSvelte } from '../ui';

const SELECTABLE_ROW_SELECTOR = '.drive-row[data-type="folder"], .drive-row[data-type="file"]';
let selectionBarMounted = false;

function emitSelectionChange() {
    window.dispatchEvent(new Event("tdrive:selectionchange"));
}

export function getRowKey(row: any) {
    if (!row) return "";
    const explicitKey = String(row.dataset.rowKey || "");
    if (explicitKey) return explicitKey;
    const type = String(row.dataset.type || "");
    const id = String(row.dataset.id || "");
    if (!type || !id) return "";
    return `${type}:${id}`;
}

function syncSelectedRowKeys() {
    setSelectedFileRowKeys(state.selectedItems.keys());
}

export function isRowSelected(row: any) {
    const key = getRowKey(row);
    return Boolean(key && state.selectedItems.has(key));
}

export function rowToSelectionItem(row: any) {
    const type = String(row?.dataset?.type || "");
    if (type === "folder") {
        return {
            type: "folder",
            id: String(row.dataset.id || ""),
            name: String(row.dataset.name || "Folder"),
            parentId: String(row.dataset.parentId || ""),
            row,
        };
    }

    return {
        type: "file",
        id: Number(row?.dataset?.id || 0),
        name: String(row?.dataset?.name || "File"),
        size: Number(row?.dataset?.size || 0),
        source: String(row?.dataset?.source || "fs"),
        parentId: String(row?.dataset?.parentId || ""),
        uploaderID: Number(row?.dataset?.uploaderId || 0),
        canDelete: row?.dataset?.canDelete !== "false",
        canRename: row?.dataset?.canRename !== "false",
        row,
    };
}

export function updateSelectionBar() {
    syncSelectedRowKeys();
    if (!state.selectionBarEl) {
        emitSelectionChange();
        return;
    }
    const count = state.selectedItems.size;
    setSelectionCount(count);
    if (!count) {
        state.selectionBarEl.style.display = "none";
        emitSelectionChange();
        return;
    }

    state.selectionBarEl.style.display = "flex";
    emitSelectionChange();
}

export function clearSelection({ keepAnchor = false } = {}) {
    state.selectedItems.clear();
    if (!keepAnchor) state.selectionAnchorIndex = -1;
    updateSelectionBar();
}

export function selectRow(row: any, rowIndex: any) {
    const key = getRowKey(row);
    if (!key) return;
    const item = rowToSelectionItem(row);
    state.selectedItems.set(key, item);
    state.selectionAnchorIndex = rowIndex;
    updateSelectionBar();
}

export function deselectRow(row: any) {
    const key = getRowKey(row);
    if (!key) return;
    state.selectedItems.delete(key);
    updateSelectionBar();
}

export function handleRowSelection(row: any, e: any) {
    if (!row) return;
    if (e?.button === 2) return;

    const list = document.getElementById("file-list");
    const rows = list ? Array.from(list.querySelectorAll(SELECTABLE_ROW_SELECTOR)) : [];
    const idx = rows.indexOf(row);
    if (idx === -1) return;

    const isToggle = Boolean(e?.metaKey || e?.ctrlKey);
    const isRange = Boolean(e?.shiftKey) && state.selectionAnchorIndex >= 0;

    if (isRange) {
        const start = Math.min(state.selectionAnchorIndex, idx);
        const end = Math.max(state.selectionAnchorIndex, idx);
        if (!isToggle) clearSelection({ keepAnchor: true });

        for (let i = start; i <= end; i++) {
            const r = rows[i];
            if (!r) continue;
            const key = getRowKey(r);
            if (!key) continue;
            if (state.selectedItems.has(key)) continue;
            const item = rowToSelectionItem(r);
            state.selectedItems.set(key, item);
        }

        updateSelectionBar();
        return;
    }

    if (isToggle) {
        if (isRowSelected(row)) {
            deselectRow(row);
        } else {
            const key = getRowKey(row);
            if (!key) return;
            const item = rowToSelectionItem(row);
            state.selectedItems.set(key, item);
            state.selectionAnchorIndex = idx;
            updateSelectionBar();
        }
        return;
    }

    clearSelection({ keepAnchor: true });
    selectRow(row, idx);
}

export function ensureRowSelectedForContextMenu(row: any) {
    if (!row) return;
    const list = document.getElementById("file-list");
    const rows = list ? Array.from(list.querySelectorAll(SELECTABLE_ROW_SELECTOR)) : [];
    const idx = rows.indexOf(row);
    if (idx === -1) return;

    if (!isRowSelected(row) || !state.selectedItems.size) {
        clearSelection({ keepAnchor: true });
        selectRow(row, idx);
        return;
    }

    state.selectionAnchorIndex = idx;
}

export function getSelectionPayload() {
    return Array.from(state.selectedItems.values()).map((item) => ({
        type: item.type,
        id: item.id,
        name: item.name,
        size: item.size,
        source: item.source,
        parentId: item.parentId,
        uploaderID: item.uploaderID || 0,
        canDelete: item.canDelete !== false,
        canRename: item.canRename !== false,
    }));
}

export function setupSelectionBar() {
    state.selectionBarEl = document.getElementById("selection-bar");
    if (!state.selectionBarEl) return;

    if (!selectionBarMounted) {
        state.selectionBarEl.replaceChildren();
        mountSvelte(SelectionBar, {
            target: state.selectionBarEl,
            props: {
                onClear: () => clearSelection(),
                onDelete: () => {
                    if (!state.selectedItems.size) return;
                    openDeleteModal({ type: "bulk", items: getSelectionPayload(), parentId: state.currentFolderId });
                },
                onMove: () => {
                    if (!state.selectedItems.size) return;
                    openMoveModal({ type: "bulk", items: getSelectionPayload(), parentId: state.currentFolderId });
                },
            },
        });
        selectionBarMounted = true;
    }

    const list = document.getElementById("file-list");
    if (list) {
        list.addEventListener("click", (e) => {
            if ((e.target as HTMLElement).closest(".drive-row")) return;
            clearSelection();
        });
    }

    window.addEventListener("keydown", (e) => {
        if (e.key === "Escape") clearSelection();
    });

    updateSelectionBar();
}
