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
import { fileListRowForElement } from '../ui/file-list/row-lookup';
import type { FileCommandItem, FileListFileRow } from '../ui/file-list/types';

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

export function buildFileContextMenuItems(row: FileListFileRow, { folderGroup = true }: RowMenuOptions = {}): ContextMenuItem[] {
    const fileID = Number.parseInt(row.id, 10);
    if (!Number.isFinite(fileID)) return [];
    const { name: fileName, size: fileSize, source: fileSource, canDelete, canRename, encrypted } = row;
    // The row captured its drive when it was rendered, so a menu still open
    // across a drive switch downloads the file it was opened on.
    const sourceChannelId = row.channelId ?? state.activeChannel?.id;

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

    const item = fileListRowForElement(row);
    if (!item) return;

    const folderGroup = !isMobilePlatform();
    let items: ContextMenuItem[];
    if (item.kind === 'folder') {
        if (!item.id) return;
        items = buildFolderContextMenuItems(item.id, item.name, { folderGroup }, item.channelId);
    } else {
        items = buildFileContextMenuItems(item, { folderGroup });
        if (!items.length) return;
    }
    showContextMenu(x, y, items, { header });
}

export function activateContextMenu(): () => void {
    const list = document.getElementById('file-list');
    if (!list) return () => {};

    const onContextMenu = (e: MouseEvent) => {
        e.preventDefault();
        // A trashed row is a real folder row with a real id, and every item on
        // the folder menu -- open, upload into, rename, delete -- would act on
        // it as if it were still in the drive. The row's own Restore and
        // Delete forever are the only things a deleted item can do.
        if (state.virtualView === 'trash') return;
        const element = (e.target as HTMLElement).closest<HTMLElement>(".drive-row");
        // A folder still being created resolves to nothing, and the background
        // menu is the right answer for a row with no identity yet.
        const row = fileListRowForElement(element);

        if (element) {
            ensureRowSelectedForContextMenu(element);
            element.focus({ preventScroll: true });
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

        if (row?.kind === "folder") {
            if (!row.id) return;
            showContextMenu(e.clientX, e.clientY, buildFolderContextMenuItems(row.id, row.name, {}, row.channelId));
            return;
        }

        if (row?.kind === "file") {
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
