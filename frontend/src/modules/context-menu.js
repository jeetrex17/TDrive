// Context menu handling for TDrive frontend

import { state } from '../state.js';
import { icons } from '../constants.js';
import { clearSelection, ensureRowSelectedForContextMenu, getSelectionPayload } from './selection.js';
import { openDeleteModal } from './modals/delete.js';
import { openRenameModal } from './modals/rename.js';
import { openMoveModal } from './modals/move.js';
import { navigateToFolder } from './navigation.js';
import { uploadWithParentID } from './transfers.js';

export function setupContextMenu() {
    const menu = document.getElementById("context-menu");
    const list = document.getElementById("file-list");
    if (!menu || !list) return;

    const hide = () => {
        menu.style.display = "none";
        menu.innerHTML = "";
    };

    const show = (x, y, items) => {
        menu.innerHTML = "";
        items.forEach((item) => {
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
        const row = e.target.closest(".drive-row");
        const type = row?.dataset?.type || "background";

        if (row) ensureRowSelectedForContextMenu(row);

        // Step 4: shared drives are flat-file only. Folder ops and "Move to..."
        // (which targets a folder) are hidden until Step 5 lands.
        const isShared = state.activeChannel?.kind === "shared";

        if (state.selectedItems.size > 1) {
            const count = state.selectedItems.size;
            const items = [];
            if (!isShared) {
                items.push({ label: `Move ${count} items…`, onClick: () => openMoveModal({ type: "bulk", items: getSelectionPayload(), parentId: state.currentFolderId }) });
            }
            items.push(
                { label: `Delete ${count} items`, danger: true, onClick: () => openDeleteModal({ type: "bulk", items: getSelectionPayload(), parentId: state.currentFolderId }) },
                { type: "divider" },
                { label: "Clear selection", onClick: () => clearSelection() },
                { label: "Refresh", onClick: () => window.triggerRefresh() },
            );
            show(e.clientX, e.clientY, items);
            return;
        }

        if (type === "folder") {
            // Folders only exist on personal; shared drives never render
            // folder rows. But guard anyway for defense-in-depth.
            if (isShared) return;
            const folderID = row.dataset.id;
            const folderName = row.dataset.name || "Folder";
            show(e.clientX, e.clientY, [
                { label: `Open "${folderName}"`, onClick: () => navigateToFolder(folderID, folderName) },
                { label: "Upload to this folder", onClick: () => uploadWithParentID(folderID) },
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
            const items = [
                { label: "Download", onClick: () => window.initDownload(fileID, fileName, fileSize) },
            ];
            const renamePayload = fileSource === "fs"
                ? { type: "file", id: fileID, name: fileName, parentId: state.currentFolderId }
                : { type: "file", id: fileID, name: fileName, size: fileSize, parentId: state.currentFolderId, source: "tg" };
            items.push({ label: "Rename…", onClick: () => openRenameModal(renamePayload) });
            if (!isShared) {
                const movePayload = fileSource === "fs"
                    ? { type: "file", id: fileID, name: fileName, parentId: state.currentFolderId }
                    : { type: "file", id: fileID, name: fileName, size: fileSize, parentId: state.currentFolderId, source: "tg" };
                items.push({ label: "Move to…", onClick: () => openMoveModal(movePayload) });
            }
            if (canDelete) {
                items.push({ label: "Delete", danger: true, onClick: () => window.initDelete(fileID, fileName) });
            }
            items.push({ type: "divider" }, { label: "Upload", onClick: () => window.selectFile() });
            if (!isShared) {
                items.push({ label: "New folder", onClick: () => window.openNewFolderModal() });
            }
            items.push({ label: "Refresh", onClick: () => window.triggerRefresh() });
            show(e.clientX, e.clientY, items);
            return;
        }

        const bgItems = [];
        if (!isShared) {
            bgItems.push({ label: "New folder", onClick: () => window.openNewFolderModal() });
        }
        bgItems.push(
            { label: "Upload", onClick: () => window.selectFile() },
            { label: "Refresh", onClick: () => window.triggerRefresh() },
        );
        show(e.clientX, e.clientY, bgItems);
    });
}
