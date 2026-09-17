// Context menu handling for TDrive frontend

import { state } from '../state';
import { isMobilePlatform } from '../api';
import { clearSelection, ensureRowSelectedForContextMenu, getSelectionPayload } from './selection';
import { openDeleteModal } from './modals/delete';
import { openRenameModal } from './modals/rename';
import { openMoveModal } from './modals/move';
import { openNewFolderModal } from './modals/folder';
import { navigateToFolder } from './navigation';
import { enqueueDownload, enqueueFolderDownload, importFolderWithParentID, uploadWithParentID } from './transfers';
import { canOpenFileViewer, isVideoFile } from './media-types';
import { appActions } from './app-actions';
import {
    hideContextMenu,
    showContextMenu,
    type ContextMenuHeader,
    type ContextMenuItem,
} from '../ui/menus/context-menu-store';
import type { FileCommandItem } from '../ui/file-list/types';

// folderGroup appends the current-folder actions (upload here, new folder,
// refresh) that make sense in the desktop popover. A phone action sheet lists
// only the item's own actions and separates the destructive one, so those
// callers pass folderGroup: false.
interface RowMenuOptions {
    folderGroup?: boolean;
}

export function buildFolderContextMenuItems(
    folderID: string,
    folderName: string,
    { folderGroup = true }: RowMenuOptions = {},
    sourceChannelId: unknown = state.activeChannel?.id,
): ContextMenuItem[] {
    // On the phone sheet the name is already in the header, so the tiles read
    // "Open" rather than repeating it back at the reader.
    const tile = !folderGroup;
    const items: ContextMenuItem[] = [
        { label: tile ? 'Open' : `Open "${folderName}"`, icon: 'open', primary: tile, action: () => navigateToFolder(folderID, folderName) },
        { label: tile ? 'Download' : `Download "${folderName}"`, icon: 'download', primary: tile, action: () => enqueueFolderDownload(folderID, folderName, 0, sourceChannelId) },
    ];
    if (folderGroup) {
        items.push(
            { label: "Upload files to this folder", action: () => uploadWithParentID(folderID) },
            { label: "Upload folder to this folder", action: () => importFolderWithParentID(folderID) },
        );
    }
    items.push(
        { label: "Rename…", icon: 'rename', primary: tile, action: () => openRenameModal({ type: "folder", id: folderID, name: folderName, parentId: state.currentFolderId }) },
        { label: "Move to…", icon: 'move', primary: tile, action: () => openMoveModal({ type: "folder", id: folderID, name: folderName, parentId: state.currentFolderId }) },
    );
    if (!folderGroup) items.push({ type: "divider" });
    items.push({ label: tile ? 'Delete' : 'Delete "' + folderName + '"', icon: 'delete', danger: true, action: () => openDeleteModal({ type: "folder", id: folderID, name: folderName }) });
    if (folderGroup) {
        items.push(
            { type: "divider" },
            { label: "New folder", action: openNewFolderModal },
            { label: "Refresh", action: () => { void appActions().triggerRefresh(); } },
        );
    }
    return items;
}

export function buildFileContextMenuItems(row: HTMLElement, { folderGroup = true }: RowMenuOptions = {}): ContextMenuItem[] {
    const fileID = parseInt(row.dataset.id || "", 10);
    if (!Number.isFinite(fileID)) return [];
    const fileName = row.dataset.name || "";
    const fileSize = Number(row.dataset.size || 0);
    const fileSource = row.dataset.source === 'tg' ? 'tg' : 'fs';
    const canDelete = row.dataset.canDelete === "true";
    const canRename = row.dataset.canRename !== "false";
    const encrypted = row.dataset.encrypted === "true";
    const sourceChannelId = row.dataset.channelId ?? state.activeChannel?.id;

    // The phone sheet promotes the everyday actions to tiles. Delete is left
    // out of that row on purpose: a destructive action does not belong under a
    // thumb reaching for Download.
    const tile = !folderGroup;

    const items: ContextMenuItem[] = [];
    if (isVideoFile(fileName)) {
        items.push({ label: "Play", icon: 'play', primary: tile, action: () => { void appActions().playVideo({ id: fileID, name: fileName, size: fileSize, encrypted }); } });
    } else if (canOpenFileViewer(fileName)) {
        items.push({ label: "Open", icon: 'open', primary: tile, action: () => { void appActions().openFile({ id: fileID, name: fileName, size: fileSize, encrypted }); } });
    }
    items.push({ label: "Download", icon: 'download', primary: tile, action: () => enqueueDownload(fileID, fileName, fileSize, sourceChannelId) });

    const fileTarget: FileCommandItem = fileSource === 'tg'
        ? { type: 'file', id: fileID, name: fileName, size: fileSize, parentId: state.currentFolderId, source: 'tg' }
        : { type: 'file', id: fileID, name: fileName, size: fileSize, parentId: state.currentFolderId, source: 'fs' };
    if (canRename) items.push({ label: 'Rename…', icon: 'rename', primary: tile, action: () => openRenameModal(fileTarget) });
    items.push({ label: 'Move to…', icon: 'move', primary: tile, action: () => openMoveModal(fileTarget) });

    if (folderGroup) {
        if (canDelete) items.push({ label: 'Delete', icon: 'delete', danger: true, action: () => openDeleteModal(fileTarget) });
        items.push(
            { type: "divider" },
            { label: "Upload files", action: () => { void uploadWithParentID(state.currentFolderId); } },
            { label: "Upload folder", action: () => { void importFolderWithParentID(state.currentFolderId); } },
            { label: "New folder", action: openNewFolderModal },
            { label: "Refresh", action: () => { void appActions().triggerRefresh(); } },
        );
    } else if (canDelete) {
        items.push({ type: "divider" }, { label: 'Delete', icon: 'delete', danger: true, action: () => openDeleteModal(fileTarget) });
    }
    return items;
}

function backgroundContextMenuItems(): ContextMenuItem[] {
    return [
        { label: "New folder", action: openNewFolderModal },
        { label: "Upload files", action: () => { void uploadWithParentID(state.currentFolderId); } },
        { label: "Upload folder", action: () => { void importFolderWithParentID(state.currentFolderId); } },
        { label: "Refresh", action: () => { void appActions().triggerRefresh(); } },
    ];
}

/**
 * Opens the menu for a file or folder row from a long-press or overflow tap.
 * The caller passes the action-sheet header (name, meta, kind) and, by default,
 * the row is not selected. On a phone the sheet lists only the item's own
 * actions; on desktop it matches the right-click popover.
 */
export function showRowContextMenu(
    row: HTMLElement,
    x: number,
    y: number,
    options: { header?: ContextMenuHeader; select?: boolean } = {},
): void {
    const { header, select = false } = options;
    if (select) ensureRowSelectedForContextMenu(row);

    const folderGroup = !isMobilePlatform();
    let items: ContextMenuItem[];
    if (row.dataset.type === 'folder') {
        const folderID = row.dataset.id || '';
        if (!folderID) return;
        items = buildFolderContextMenuItems(folderID, row.dataset.name || 'Folder', { folderGroup }, row.dataset.channelId);
    } else if (row.dataset.type === 'file') {
        items = buildFileContextMenuItems(row, { folderGroup });
        if (!items.length) return;
    } else {
        return;
    }
    showContextMenu(x, y, items, { header });
}

export function activateContextMenu(): () => void {
    const list = document.getElementById('file-list');
    if (!list) return () => {};

    const onContextMenu = (e: MouseEvent) => {
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
            showContextMenu(e.clientX, e.clientY, buildFolderContextMenuItems(folderID, row.dataset.name || "Folder", {}, row.dataset.channelId));
            return;
        }

        if (type === "file" && row) {
            const items = buildFileContextMenuItems(row);
            if (!items.length) return;
            showContextMenu(e.clientX, e.clientY, items);
            return;
        }

        showContextMenu(e.clientX, e.clientY, backgroundContextMenuItems());
    };

    list.addEventListener('contextmenu', onContextMenu);
    return () => {
        list.removeEventListener('contextmenu', onContextMenu);
        hideContextMenu();
    };
}

export { hideContextMenu, showContextMenu };
export type { ContextMenuItem };
