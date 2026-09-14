// Context menu handling for TDrive frontend

import { state } from '../state';
import { clearSelection, ensureRowSelectedForContextMenu, getSelectionPayload } from './selection';
import { openDeleteModal } from './modals/delete';
import { openRenameModal } from './modals/rename';
import { openMoveModal } from './modals/move';
import { openNewFolderModal } from './modals/folder';
import { navigateToFolder } from './navigation';
import { enqueueDownload, enqueueFolderDownload, importFolderWithParentID, uploadWithParentID } from './transfers';
import { canOpenFileViewer, isVideoFile } from './media-types';
import { appActions } from './app-actions';
import ContextMenu from '../ui/menus/ContextMenu.svelte';
import { hideContextMenu, showContextMenu, type ContextMenuItem } from '../ui/menus/context-menu-store';
import type { FileCommandItem } from '../ui/file-list/types';
import { mountSvelte } from '../ui';

let contextMenuMounted = false;

export function buildFolderContextMenuItems(folderID: string, folderName: string): ContextMenuItem[] {
    return [
        { label: `Open "${folderName}"`, action: () => navigateToFolder(folderID, folderName) },
        { label: `Download "${folderName}"`, action: () => enqueueFolderDownload(folderID, folderName) },
        { label: "Upload files to this folder", action: () => uploadWithParentID(folderID) },
        { label: "Upload folder to this folder", action: () => importFolderWithParentID(folderID) },
        { label: "Rename…", action: () => openRenameModal({ type: "folder", id: folderID, name: folderName, parentId: state.currentFolderId }) },
        { label: "Move to…", action: () => openMoveModal({ type: "folder", id: folderID, name: folderName, parentId: state.currentFolderId }) },
        { label: 'Delete "' + folderName + '"', danger: true, action: () => openDeleteModal({ type: "folder", id: folderID, name: folderName }) },
        { type: "divider" },
        { label: "New folder", action: openNewFolderModal },
        { label: "Refresh", action: () => { void appActions().triggerRefresh(); } },
    ];
}

function mountContextMenu(menu: HTMLElement) {
    if (contextMenuMounted) return;
    menu.replaceChildren();
    mountSvelte(ContextMenu, { target: menu, props: {} });
    contextMenuMounted = true;
}

export function setupContextMenu() {
    const menu = document.getElementById("context-menu");
    const list = document.getElementById("file-list");
    if (!menu || !list) return;
    mountContextMenu(menu);

    list.addEventListener("contextmenu", (e) => {
        e.preventDefault();
        const row = (e.target as HTMLElement).closest<HTMLElement>(".drive-row");
        const type = row?.dataset?.type || "background";

        if (row) {
            ensureRowSelectedForContextMenu(row);
            row.focus({ preventScroll: true });
        } else {
            list.focus({ preventScroll: true });
        }

        if (state.selectedItems.size > 1) {
            const count = state.selectedItems.size;
            showContextMenu(e.clientX, e.clientY, [
                { label: `Move ${count} items…`, action: () => openMoveModal({ type: "bulk", items: getSelectionPayload(), parentId: state.currentFolderId }) },
                { label: `Delete ${count} items`, danger: true, action: () => openDeleteModal({ type: "bulk", items: getSelectionPayload(), parentId: state.currentFolderId }) },
                { type: "divider" },
                { label: "Clear selection", action: () => clearSelection() },
                { label: "Refresh", action: () => { void appActions().triggerRefresh(); } },
            ]);
            return;
        }

        if (type === "folder" && row) {
            const folderID = row.dataset.id || "";
            if (!folderID) return;
            const folderName = row.dataset.name || "Folder";
            showContextMenu(e.clientX, e.clientY, buildFolderContextMenuItems(folderID, folderName));
            return;
        }

        if (type === "file" && row) {
            const fileID = parseInt(row.dataset.id || "", 10);
            if (!Number.isFinite(fileID)) return;
            const fileName = row.dataset.name || "";
            const fileSize = Number(row.dataset.size || 0);
            const fileSource = row.dataset.source === 'tg' ? 'tg' : 'fs';
            const canDelete = row.dataset.canDelete === "true";
            const canRename = row.dataset.canRename !== "false";
            const encrypted = row.dataset.encrypted === "true";
            const items: ContextMenuItem[] = [
                { label: "Download", action: () => enqueueDownload(fileID, fileName, fileSize) },
            ];
            if (isVideoFile(fileName)) {
                items.unshift({ label: "Play", action: () => { void appActions().playVideo({ id: fileID, name: fileName, size: fileSize, encrypted }); } });
            } else if (canOpenFileViewer(fileName)) {
                items.unshift({ label: "Open", action: () => { void appActions().openFile({ id: fileID, name: fileName, size: fileSize, encrypted }); } });
            }
            const fileTarget: FileCommandItem = fileSource === 'tg'
                ? {
                    type: 'file',
                    id: fileID,
                    name: fileName,
                    size: fileSize,
                    parentId: state.currentFolderId,
                    source: 'tg',
                }
                : {
                    type: 'file',
                    id: fileID,
                    name: fileName,
                    size: fileSize,
                    parentId: state.currentFolderId,
                    source: 'fs',
                };
            if (canRename) {
                items.push({ label: 'Rename…', action: () => openRenameModal(fileTarget) });
            }
            items.push({ label: 'Move to…', action: () => openMoveModal(fileTarget) });
            if (canDelete) {
                items.push({ label: 'Delete', danger: true, action: () => openDeleteModal(fileTarget) });
            }
            items.push(
                { type: "divider" },
                { label: "Upload files", action: () => { void uploadWithParentID(state.currentFolderId); } },
                { label: "Upload folder", action: () => { void importFolderWithParentID(state.currentFolderId); } },
                { label: "New folder", action: openNewFolderModal },
                { label: "Refresh", action: () => { void appActions().triggerRefresh(); } },
            );
            showContextMenu(e.clientX, e.clientY, items);
            return;
        }

        showContextMenu(e.clientX, e.clientY, [
            { label: "New folder", action: openNewFolderModal },
            { label: "Upload files", action: () => { void uploadWithParentID(state.currentFolderId); } },
            { label: "Upload folder", action: () => { void importFolderWithParentID(state.currentFolderId); } },
            { label: "Refresh", action: () => { void appActions().triggerRefresh(); } },
        ]);
    });
}

export { hideContextMenu, showContextMenu };
export type { ContextMenuItem };
