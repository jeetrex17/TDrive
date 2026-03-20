// Selection handling module for TDrive frontend

import { state } from '../state.js';
import { openDeleteModal } from './modals/delete.js';
import { openMoveModal } from './modals/move.js';

function emitSelectionChange() {
    window.dispatchEvent(new Event("tdrive:selectionchange"));
}

export function getRowKey(row) {
    if (!row) return "";
    const type = String(row.dataset.type || "");
    const id = String(row.dataset.id || "");
    if (!type || !id) return "";
    return `${type}:${id}`;
}

export function rowToSelectionItem(row) {
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
        row,
    };
}

export function setRowSelected(row, selected) {
    if (!row) return;
    row.classList.toggle("is-selected", Boolean(selected));
    row.setAttribute("aria-selected", selected ? "true" : "false");
}

export function updateSelectionBar() {
    if (!state.selectionBarEl || !state.selectionCountEl) {
        emitSelectionChange();
        return;
    }
    const count = state.selectedItems.size;
    if (!count) {
        state.selectionBarEl.style.display = "none";
        emitSelectionChange();
        return;
    }

    state.selectionBarEl.style.display = "flex";
    state.selectionCountEl.textContent = count === 1 ? "1 selected" : `${count} selected`;
    emitSelectionChange();
}

export function clearSelection({ keepAnchor = false } = {}) {
    for (const item of state.selectedItems.values()) {
        if (item?.row) setRowSelected(item.row, false);
    }
    state.selectedItems.clear();
    if (!keepAnchor) state.selectionAnchorIndex = -1;
    updateSelectionBar();
}

export function selectRow(row, rowIndex) {
    const key = getRowKey(row);
    if (!key) return;
    const item = rowToSelectionItem(row);
    state.selectedItems.set(key, item);
    setRowSelected(row, true);
    state.selectionAnchorIndex = rowIndex;
    updateSelectionBar();
}

export function deselectRow(row) {
    const key = getRowKey(row);
    if (!key) return;
    state.selectedItems.delete(key);
    setRowSelected(row, false);
    updateSelectionBar();
}

export function handleRowSelection(row, e) {
    if (!row) return;
    if (e?.button === 2) return;

    const list = document.getElementById("file-list");
    const rows = list ? Array.from(list.querySelectorAll(".drive-row")) : [];
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
            setRowSelected(r, true);
        }

        updateSelectionBar();
        return;
    }

    if (isToggle) {
        if (row.classList.contains("is-selected")) {
            deselectRow(row);
        } else {
            const item = rowToSelectionItem(row);
            state.selectedItems.set(getRowKey(row), item);
            setRowSelected(row, true);
            state.selectionAnchorIndex = idx;
            updateSelectionBar();
        }
        return;
    }

    clearSelection({ keepAnchor: true });
    selectRow(row, idx);
}

export function ensureRowSelectedForContextMenu(row) {
    if (!row) return;
    const list = document.getElementById("file-list");
    const rows = list ? Array.from(list.querySelectorAll(".drive-row")) : [];
    const idx = rows.indexOf(row);
    if (idx === -1) return;

    if (!row.classList.contains("is-selected") || !state.selectedItems.size) {
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
    }));
}

export function setupSelectionBar() {
    state.selectionBarEl = document.getElementById("selection-bar");
    state.selectionCountEl = document.getElementById("selection-count");
    state.selectionMoveBtnEl = document.getElementById("selection-move");
    state.selectionDeleteBtnEl = document.getElementById("selection-delete");
    state.selectionClearBtnEl = document.getElementById("selection-clear");
    if (!state.selectionBarEl || !state.selectionCountEl) return;

    if (state.selectionClearBtnEl) {
        state.selectionClearBtnEl.addEventListener("click", () => clearSelection());
    }
    if (state.selectionDeleteBtnEl) {
        state.selectionDeleteBtnEl.addEventListener("click", () => {
            if (!state.selectedItems.size) return;
            openDeleteModal({ type: "bulk", items: getSelectionPayload(), parentId: state.currentFolderId });
        });
    }
    if (state.selectionMoveBtnEl) {
        state.selectionMoveBtnEl.addEventListener("click", () => {
            if (!state.selectedItems.size) return;
            openMoveModal({ type: "bulk", items: getSelectionPayload(), parentId: state.currentFolderId });
        });
    }

    const list = document.getElementById("file-list");
    if (list) {
        list.addEventListener("click", (e) => {
            if (e.target.closest(".drive-row")) return;
            clearSelection();
        });
    }

    window.addEventListener("keydown", (e) => {
        if (e.key === "Escape") clearSelection();
    });

    updateSelectionBar();
}
