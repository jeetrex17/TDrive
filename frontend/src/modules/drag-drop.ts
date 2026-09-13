// Drag and drop handling for TDrive frontend

import { state } from '../state';
import {
    addTelegramFileToDrive,
    moveFile,
    moveFolder,
    setFileDropEnabled,
    type OperationResult,
} from '../api';
import { callWithPasswordRetry } from './modals/encryption-password';
import { notify } from './notifications';
import { humanizeBackendError } from './errors';
import { appActions } from './app-actions';
import type { FileCommandItem } from '../ui/file-list/types';

export function clearDropHighlights(): void {
    if (state.dragOverEl) {
        state.dragOverEl.classList.remove('drop-target');
        state.dragOverEl.classList.remove('drop-denied');
        state.dragOverEl = null;
    }
    if (state.dragRootEl) {
        state.dragRootEl.classList.remove('drop-target');
        state.dragRootEl.classList.remove('drop-denied');
    }
}

export function setDropHighlight(element: HTMLElement, allowed: boolean): void {
    if (state.dragOverEl && state.dragOverEl !== element) {
        state.dragOverEl.classList.remove('drop-target');
        state.dragOverEl.classList.remove('drop-denied');
    }
    state.dragOverEl = element;
    element.classList.toggle('drop-target', allowed);
    element.classList.toggle('drop-denied', !allowed);
}

export function canDropOnFolder(targetFolderId: string): boolean {
    const drag = state.dragState;
    if (!drag || targetFolderId === drag.parentId) return false;
    return !drag.blocked.has(targetFolderId);
}

async function ensureFileInTdriveSystem(target: FileCommandItem): Promise<void> {
    if (target.type !== 'file' || target.source !== 'tg') return;
    const result = await addTelegramFileToDrive(
        target.id,
        target.name,
        target.size,
        target.parentId,
    );
    if (!result.ok) throw new Error(humanizeBackendError(result.error));
}

export async function performDropMove(newParentId: string): Promise<void> {
    const drag = state.dragState;
    if (!drag) return;
    const items = drag.items;
    if (items.length === 0 || newParentId === drag.parentId) {
        clearDropHighlights();
        return;
    }

    let failures = 0;
    let lastError = '';
    for (const item of items) {
        try {
            let result: OperationResult;
            if (item.type === 'folder') {
                result = await callWithPasswordRetry(() => moveFolder(item.id, newParentId));
            } else {
                await ensureFileInTdriveSystem(item);
                result = await callWithPasswordRetry(() => moveFile(item.id, newParentId));
            }
            if (!result.ok) {
                failures += 1;
                lastError = humanizeBackendError(result.error);
            }
        } catch (error) {
            failures += 1;
            lastError = humanizeBackendError(error);
        }
    }

    if (failures > 0) {
        notify({
            level: 'error',
            title: failures === items.length ? 'Move failed' : `${failures} of ${items.length} moves failed`,
            body: lastError,
        });
    }
    appActions().refreshFiles();
    clearDropHighlights();
}

function setNativeFileDrop(enabled: boolean): void {
    void setFileDropEnabled(enabled).catch(() => {
        // Browser-only development has no native drop binding.
    });
}

function clearDragRows(): void {
    const drag = state.dragState;
    if (!drag) return;
    drag.row.classList.remove('is-dragging');
    for (const item of drag.items) item.row?.classList.remove('is-dragging');
}

export function beginRowDrag(
    row: HTMLElement,
    items: FileCommandItem[],
    parentId: string,
    blocked: Set<string> = new Set<string>(),
): void {
    clearDropHighlights();
    clearDragRows();
    setNativeFileDrop(false);
    state.dragState = { items, parentId, blocked, row };
    for (const item of items) item.row?.classList.add('is-dragging');
    row.classList.add('is-dragging');
}

export function endRowDrag(): void {
    clearDragRows();
    state.dragState = null;
    clearDropHighlights();
    setNativeFileDrop(true);
}
