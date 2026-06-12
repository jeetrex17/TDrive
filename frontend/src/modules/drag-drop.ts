// Drag and drop handling for TDrive frontend

import { state } from '../state';
import { MoveFile, MoveFolder, MsgToTdriveSystem } from '../../wailsjs/go/main/App';
import { callWithPasswordRetry } from './modals/encryption-password';
import { notify } from './notifications';
import { humanizeBackendError } from './errors';

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

export function setDropHighlight(el: HTMLElement, allowed: boolean) {
    if (state.dragOverEl && state.dragOverEl !== el) {
        state.dragOverEl.classList.remove("drop-target");
        state.dragOverEl.classList.remove("drop-denied");
    }
    state.dragOverEl = el;
    el.classList.toggle("drop-target", Boolean(allowed));
    el.classList.toggle("drop-denied", !allowed);
}

export function canDropOnFolder(targetFolderId: string) {
    if (!state.dragState) return false;
    const target = String(targetFolderId || "");
    if (target === String(state.dragState.parentId || "")) return false;
    // blocked holds every dragged folder id plus its descendants, so we never
    // drop a folder into itself or its own subtree.
    if (state.dragState.blocked && state.dragState.blocked.has(target)) return false;
    return true;
}

async function ensureFileInTdriveSystem(target: any) {
    if (!target || target.type !== "file") return;
    if (String(target.source || "fs") !== "tg") return;

    const res = await MsgToTdriveSystem(
        Number(target.id),
        String(target.name || ""),
        Number(target.size || 0),
        String(target.parentId || "")
    );

    if (typeof res === "string" && res.startsWith("Error")) {
        throw new Error(humanizeBackendError(res));
    }
}

export async function performDropMove(newParentId: string) {
    if (!state.dragState) return;
    const items = Array.isArray(state.dragState.items) ? state.dragState.items : [];
    const parent = String(newParentId || "");
    if (!items.length || parent === String(state.dragState.parentId || "")) {
        clearDropHighlights();
        return;
    }

    let failures = 0;
    let lastError = "";
    for (const item of items) {
        try {
            let res = "";
            if (item.type === "folder") {
                res = await callWithPasswordRetry(() => MoveFolder(String(item.id), parent));
            } else {
                await ensureFileInTdriveSystem({
                    type: "file",
                    id: Number(item.id),
                    name: item.name,
                    size: item.size,
                    parentId: item.parentId,
                    source: item.source,
                });
                res = await callWithPasswordRetry(() => MoveFile(Number(item.id), parent));
            }
            if (typeof res === "string" && res.startsWith("Error")) {
                failures++;
                lastError = humanizeBackendError(res);
            }
        } catch (err) {
            failures++;
            lastError = humanizeBackendError(err);
        }
    }

    if (failures > 0) {
        notify({
            level: 'error',
            title: failures === items.length ? 'Move failed' : `${failures} of ${items.length} moves failed`,
            body: lastError,
        });
    }
    window.refreshFiles();
    clearDropHighlights();
}

function setNativeFileDrop(enabled: boolean) {
    // macOS intercepts the in-app HTML5 drag while the native OS file-drop target
    // is live, breaking the move and popping the upload dialog. Turn it off for
    // the duration of an internal drag.
    try { (window as any)?.go?.main?.App?.SetFileDropEnabled?.(enabled); } catch { /* binding optional */ }
}

function clearDragRows() {
    if (!state.dragState) return;
    if (state.dragState.row) state.dragState.row.classList.remove("is-dragging");
    if (Array.isArray(state.dragState.items)) {
        for (const it of state.dragState.items) {
            if (it?.row) it.row.classList.remove("is-dragging");
        }
    }
}

export function beginRowDrag(row: HTMLElement, items: any[], parentId: string, blocked?: Set<string>) {
    clearDropHighlights();
    clearDragRows();
    setNativeFileDrop(false);
    state.dragState = { items, parentId: String(parentId || ""), blocked: blocked || new Set<string>(), row };
    for (const it of items) {
        if (it?.row) it.row.classList.add("is-dragging");
    }
    row.classList.add("is-dragging");
}

export function endRowDrag() {
    clearDragRows();
    state.dragState = null;
    clearDropHighlights();
    setNativeFileDrop(true);
}
