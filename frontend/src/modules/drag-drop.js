// Drag and drop handling for TDrive frontend

import { state } from '../state.js';
import { MoveFile, MoveFolder, MsgToTdriveSystem } from '../../wailsjs/go/main/App';

export function clearDropHighlights() {
    if (state.dragOverEl) {
        state.dragOverEl.classList.remove("drop-target");
        state.dragOverEl.classList.remove("drop-denied");
        state.dragOverEl = null;
    }
    if (state.dragRootEl) {
        state.dragRootEl.classList.remove("drop-target");
        state.dragRootEl.classList.remove("drop-denied");
    }
}

export function setDropHighlight(el, allowed) {
    if (state.dragOverEl && state.dragOverEl !== el) {
        state.dragOverEl.classList.remove("drop-target");
        state.dragOverEl.classList.remove("drop-denied");
    }
    state.dragOverEl = el;
    el.classList.toggle("drop-target", Boolean(allowed));
    el.classList.toggle("drop-denied", !allowed);
}

export function canDropOnFolder(targetFolderId) {
    if (!state.dragState) return false;
    const target = String(targetFolderId || "");
    const currentParent = String(state.dragState.parentId || "");
    if (target === currentParent) return false;

    if (state.dragState.type === "folder") {
        const movingId = String(state.dragState.id || "");
        if (!movingId) return false;
        if (target === movingId) return false;
        if (state.dragState.blocked && state.dragState.blocked.has(target)) return false;
    }
    return true;
}

async function ensureFileInTdriveSystem(target) {
    if (!target || target.type !== "file") return;
    if (String(target.source || "fs") !== "tg") return;

    const res = await MsgToTdriveSystem(
        Number(target.id),
        String(target.name || ""),
        Number(target.size || 0),
        String(target.parentId || "")
    );

    if (typeof res === "string" && res.startsWith("Error")) {
        throw new Error(res);
    }
}

export async function performDropMove(newParentId) {
    if (!state.dragState) return;
    const dragData = state.dragState;
    const parent = String(newParentId || "");
    const currentParent = String(dragData.parentId || "");
    if (parent === currentParent) return;

    try {
        let res = "";
        if (dragData.type === "folder") {
            res = await MoveFolder(String(dragData.id), parent);
        } else {
            await ensureFileInTdriveSystem({
                type: "file",
                id: Number(dragData.id),
                name: dragData.name,
                size: dragData.size,
                parentId: dragData.parentId,
                source: dragData.source,
            });
            res = await MoveFile(Number(dragData.id), parent);
        }

        if (typeof res === "string" && res.startsWith("Error")) {
            alert(res);
        } else {
            window.refreshFiles();
        }
    } finally {
        clearDropHighlights();
    }
}

export function beginRowDrag(row, nextState) {
    clearDropHighlights();
    if (state.dragState?.row) state.dragState.row.classList.remove("is-dragging");
    state.dragState = { ...nextState, row };
    row.classList.add("is-dragging");
}

export function endRowDrag() {
    if (state.dragState?.row) state.dragState.row.classList.remove("is-dragging");
    state.dragState = null;
    clearDropHighlights();
}
