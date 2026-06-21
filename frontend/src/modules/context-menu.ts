// Context menu handling for TDrive frontend

import { state } from '../state';
import { clearSelection, ensureRowSelectedForContextMenu, getSelectionPayload } from './selection';
import { openDeleteModal } from './modals/delete';
import { openRenameModal } from './modals/rename';
import { openMoveModal } from './modals/move';
import { navigateToFolder } from './navigation';
import { importFolderWithParentID, uploadWithParentID } from './transfers';
import { isVideoFile } from './media-types';

export function setupContextMenu() {
    const menu = document.getElementById("context-menu");
    const list = document.getElementById("file-list");
    if (!menu || !list) return;

    const hide = () => {
        menu.style.display = "none";
        menu.innerHTML = "";
    };

    const show = (x: number, y: number, items: any[]) => {
        menu.innerHTML = "";
        items.forEach((item: any) => {
            if (item.type === "divider") {
                const div = document.createElement("div");
                div.className = "divider";
                menu.appendChild(div);
                return;
            }

            const btn = document.createElement("button");
            btn.type = "button";
            btn.textContent = item.label;
            if (item.danger) btn.classList.add("danger");
            btn.addEventListener("click", () => {
                hide();
                item.onClick();
            });
            menu.appendChild(btn);
        });

        // Clamp within viewport
        menu.style.display = "block";
        menu.style.left = `${x}px`;
        menu.style.top = `${y}px`;

        const rect = menu.getBoundingClientRect();
        const maxX = window.innerWidth - rect.width - 8;
        const maxY = window.innerHeight - rect.height - 8;
        menu.style.left = `${Math.max(8, Math.min(x, maxX))}px`;
        menu.style.top = `${Math.max(8, Math.min(y, maxY))}px`;
    };

    document.addEventListener("click", hide);
    window.addEventListener("keydown", (e) => {
        if (e.key === "Escape") hide();
    });

    list.addEventListener("contextmenu", (e) => {
        e.preventDefault();
        const row = (e.target as HTMLElement).closest(".drive-row") as any;
        const type = row?.dataset?.type || "background";

        if (row) ensureRowSelectedForContextMenu(row);

        if (state.selectedItems.size > 1) {
            const count = state.selectedItems.size;
            show(e.clientX, e.clientY, [
                { label: `Move ${count} items…`, onClick: () => openMoveModal({ type: "bulk", items: getSelectionPayload(), parentId: state.currentFolderId }) },
                { label: `Delete ${count} items`, danger: true, onClick: () => openDeleteModal({ type: "bulk", items: getSelectionPayload(), parentId: state.currentFolderId }) },
                { type: "divider" },
                { label: "Clear selection", onClick: () => clearSelection() },
                { label: "Refresh", onClick: () => window.triggerRefresh() },
            ]);
            return;
        }

        if (type === "folder") {
            const folderID = row.dataset.id;
            const folderName = row.dataset.name || "Folder";
            show(e.clientX, e.clientY, [
                { label: `Open "${folderName}"`, onClick: () => navigateToFolder(folderID, folderName) },
                { label: "Upload files to this folder", onClick: () => uploadWithParentID(folderID) },
                { label: "Upload folder to this folder", onClick: () => importFolderWithParentID(folderID) },
                { label: "Rename…", onClick: () => openRenameModal({ type: "folder", id: folderID, name: folderName, parentId: state.currentFolderId }) },
                { label: "Move to…", onClick: () => openMoveModal({ type: "folder", id: folderID, name: folderName, parentId: state.currentFolderId }) },
                { label: `Delete "${folderName}"`, danger: true, onClick: () => window.initDeleteFolder(folderID, folderName) },
                { type: "divider" },
                { label: "New folder", onClick: () => window.openNewFolderModal() },
                { label: "Refresh", onClick: () => window.triggerRefresh() },
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
            const items: any[] = [
                { label: "Download", onClick: () => window.initDownload(fileID, fileName, fileSize) },
            ];
            if (isVideoFile(fileName)) {
                items.unshift({ label: "Play", onClick: () => window.initVideoPlayback(fileID, fileName, fileSize, encrypted) });
            }
            if (canRename) {
                const renamePayload = fileSource === "fs"
                    ? { type: "file", id: fileID, name: fileName, parentId: state.currentFolderId }
                    : { type: "file", id: fileID, name: fileName, size: fileSize, parentId: state.currentFolderId, source: "tg" };
                items.push({ label: "Rename…", onClick: () => openRenameModal(renamePayload) });
            }
            const movePayload = fileSource === "fs"
                ? { type: "file", id: fileID, name: fileName, parentId: state.currentFolderId }
                : { type: "file", id: fileID, name: fileName, size: fileSize, parentId: state.currentFolderId, source: "tg" };
            items.push({ label: "Move to…", onClick: () => openMoveModal(movePayload) });
            if (canDelete) {
                items.push({ label: "Delete", danger: true, onClick: () => window.initDelete(fileID, fileName) });
            }
            items.push(
                { type: "divider" },
                { label: "Upload files", onClick: () => window.selectFile() },
                { label: "Upload folder", onClick: () => importFolderWithParentID(state.currentFolderId) },
                { label: "New folder", onClick: () => window.openNewFolderModal() },
                { label: "Refresh", onClick: () => window.triggerRefresh() },
            );
            show(e.clientX, e.clientY, items);
            return;
        }

        show(e.clientX, e.clientY, [
            { label: "New folder", onClick: () => window.openNewFolderModal() },
            { label: "Upload files", onClick: () => window.selectFile() },
            { label: "Upload folder", onClick: () => importFolderWithParentID(state.currentFolderId) },
            { label: "Refresh", onClick: () => window.triggerRefresh() },
        ]);
    });
}
