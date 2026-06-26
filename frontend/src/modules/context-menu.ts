// Context menu handling for TDrive frontend

import { state } from '../state';
import { clearSelection, ensureRowSelectedForContextMenu, getSelectionPayload } from './selection';
import { openDeleteModal } from './modals/delete';
import { openRenameModal } from './modals/rename';
import { openMoveModal } from './modals/move';
import { navigateToFolder } from './navigation';
import { importFolderWithParentID, uploadWithParentID } from './transfers';
import { isVideoFile } from './media-types';
import ContextMenu from '../ui/menus/ContextMenu.svelte';
import { hideContextMenu, showContextMenu, type ContextMenuItem } from '../ui/menus/context-menu-store';
import { mountSvelte } from '../ui';

let contextMenuMounted = false;

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
        const row = (e.target as HTMLElement).closest(".drive-row") as any;
        const type = row?.dataset?.type || "background";

        if (row) ensureRowSelectedForContextMenu(row);

        if (state.selectedItems.size > 1) {
            const count = state.selectedItems.size;
            showContextMenu(e.clientX, e.clientY, [
                { label: `Move ${count} items…`, action: () => openMoveModal({ type: "bulk", items: getSelectionPayload(), parentId: state.currentFolderId }) },
                { label: `Delete ${count} items`, danger: true, action: () => openDeleteModal({ type: "bulk", items: getSelectionPayload(), parentId: state.currentFolderId }) },
                { type: "divider" },
                { label: "Clear selection", action: () => clearSelection() },
                { label: "Refresh", action: () => window.triggerRefresh() },
            ]);
            return;
        }

        if (type === "folder") {
            const folderID = row.dataset.id;
            const folderName = row.dataset.name || "Folder";
            showContextMenu(e.clientX, e.clientY, [
                { label: `Open "${folderName}"`, action: () => navigateToFolder(folderID, folderName) },
                { label: "Upload files to this folder", action: () => uploadWithParentID(folderID) },
                { label: "Upload folder to this folder", action: () => importFolderWithParentID(folderID) },
                { label: "Rename…", action: () => openRenameModal({ type: "folder", id: folderID, name: folderName, parentId: state.currentFolderId }) },
                { label: "Move to…", action: () => openMoveModal({ type: "folder", id: folderID, name: folderName, parentId: state.currentFolderId }) },
                { label: `Delete "${folderName}"`, danger: true, action: () => window.initDeleteFolder(folderID, folderName) },
                { type: "divider" },
                { label: "New folder", action: () => window.openNewFolderModal() },
                { label: "Refresh", action: () => window.triggerRefresh() },
            ]);
            return;
        }

        if (type === "file") {
            const fileID = parseInt(row.dataset.id, 10);
            const fileName = row.dataset.name || "";
            const fileSize = Number(row.dataset.size || 0);
            const fileSource = row.dataset.source || "fs";
            const canDelete = row.dataset.canDelete === "true";
            const canRename = row.dataset.canRename !== "false";
            const encrypted = row.dataset.encrypted === "true";
            const items: ContextMenuItem[] = [
                { label: "Download", action: () => window.initDownload(fileID, fileName, fileSize) },
            ];
            if (isVideoFile(fileName)) {
                items.unshift({ label: "Play", action: () => window.initVideoPlayback(fileID, fileName, fileSize, encrypted) });
            }
            if (canRename) {
                const renamePayload = fileSource === "fs"
                    ? { type: "file", id: fileID, name: fileName, parentId: state.currentFolderId }
                    : { type: "file", id: fileID, name: fileName, size: fileSize, parentId: state.currentFolderId, source: "tg" };
                items.push({ label: "Rename…", action: () => openRenameModal(renamePayload) });
            }
            const movePayload = fileSource === "fs"
                ? { type: "file", id: fileID, name: fileName, parentId: state.currentFolderId }
                : { type: "file", id: fileID, name: fileName, size: fileSize, parentId: state.currentFolderId, source: "tg" };
            items.push({ label: "Move to…", action: () => openMoveModal(movePayload) });
            if (canDelete) {
                items.push({ label: "Delete", danger: true, action: () => window.initDelete(fileID, fileName) });
            }
            items.push(
                { type: "divider" },
                { label: "Upload files", action: () => window.selectFile() },
                { label: "Upload folder", action: () => importFolderWithParentID(state.currentFolderId) },
                { label: "New folder", action: () => window.openNewFolderModal() },
                { label: "Refresh", action: () => window.triggerRefresh() },
            );
            showContextMenu(e.clientX, e.clientY, items);
            return;
        }

        showContextMenu(e.clientX, e.clientY, [
            { label: "New folder", action: () => window.openNewFolderModal() },
            { label: "Upload files", action: () => window.selectFile() },
            { label: "Upload folder", action: () => importFolderWithParentID(state.currentFolderId) },
            { label: "Refresh", action: () => window.triggerRefresh() },
        ]);
    });
}

export { hideContextMenu, showContextMenu };
export type { ContextMenuItem };
