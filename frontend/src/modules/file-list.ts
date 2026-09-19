// File list rendering for TDrive frontend

import { state, resetFolderCaches } from '../state';
import { splitNameAndExt, formatDate, formatBytes } from '../utils';
import { isOffline } from './connectivity';
import { humanizeBackendError } from './errors';
import { tick } from 'svelte';
import { get } from 'svelte/store';
import { breadcrumbPath } from '../ui/chrome/breadcrumb-store';
import type { ContextMenuDetail } from '../ui/menus/context-menu-store';
import { clearSelection, deselectRow, handleRowSelection, isRowSelected, reconcileSelection, selectRow, getRowKey } from './selection';
import { openRenameModal } from './modals/rename';
import { openDeleteModal } from './modals/delete';
import { openNewFolderModal } from './modals/folder';
import { navigateToFolder } from './navigation';
import { beginRowDrag, endRowDrag, canDropOnFolder, setDropHighlight, performDropMove } from './drag-drop';
import {
    getAllFsMsgIds,
    getFileList,
    getFolderContents as apiGetFolderContents,
    getStorageUsed,
    isMobilePlatform,
} from '../api';
import { calculateVisibleFolderStats } from './drive-data';
import type { FileItem, FolderItem, FolderStat, RootFile } from '../types';
import { refreshFolderIndex, collectDescendants } from './folder-index';
import { chooseFilesForCurrentFolder, enqueueDownload, enqueueFolderDownload } from './transfers';
import { ensureUserNames, uploaderChipLabel } from './uploaders';
import { renderGallery, setPhotosMode } from './gallery';
import { renderTrashRows, setTrashMode } from './trash/view';
import { canOpenFileViewer, isImageFile, isVideoFile } from './media-types';
import { appActions, type RefreshFilesOptions } from './app-actions';
import { getInteractiveFileListRows, showFileListRows, showFileListState, updateFileListRows, type InteractiveFileListRow } from '../ui/file-list/file-list-store';
import { fileListRowForElement } from '../ui/file-list/row-lookup';
import { setActiveFileRowKey } from '../ui/file-list/row-state-store';
import { rowMetaLine } from '../ui/file-list/row-meta';
import { bindLongPress, bindPullToRefresh } from '../ui/file-list/touch';
import { bindSwipeActions } from '../ui/file-list/swipe-actions';
import { openMoveModal } from './modals/move';
import { showRowContextMenu } from './context-menu';
import type { FileCommandItem, FileListAction, FileListFileRow, FileListRow, FileListUploaderChip, FileThumbnailIdentity, FolderCommandItem, FolderListRow, PendingFolderListRow } from '../ui/file-list/types';

type FileRowInput = {
    id?: string | number;
    msgId?: string | number;
    name?: string;
    size?: number;
    date?: number;
    uploadTime?: number;
    uploaderID?: number;
    uploaderId?: number;
    encrypted?: boolean;
    canDelete?: boolean;
    canRename?: boolean;
    source?: string;
};

type FolderRowInput = Pick<FolderItem, 'id' | 'name'> & Partial<Pick<FolderItem, 'parentId'>>;

type FileViewIdentity = {
    channelId: number;
    folderId: string;
};

type FileRefreshPresentation = 'foreground-navigation' | 'same-view-refresh';

type FileRefreshRequest = {
    token: number;
    view: FileViewIdentity;
    presentation: FileRefreshPresentation;
    folderEpoch: number;
};

type LoadedFileData = {
    folders: FolderItem[];
    filesystemFiles: FileItem[];
    telegramFiles: RootFile[];
    filesystemMessageIds: Set<number>;
    folderStats: Map<string, FolderStat>;
};

/**
 * What addresses a file's thumbnail, or undefined when the file has none to
 * address. Videos count: the backend draws a frame for them at upload and
 * serves it through the same rendition, so a row or an album cover that stops
 * at images would be refusing a picture that already exists.
 */
export function fileThumbnailIdentity(
    file: Pick<FileItem, 'msgId' | 'name' | 'revision'>,
    channelId: number,
): FileThumbnailIdentity | undefined {
    if (!(isImageFile(file.name) || isVideoFile(file.name))
        || !Number.isSafeInteger(channelId)
        || channelId <= 0
        || !Number.isSafeInteger(file.msgId)
        || file.msgId <= 0
        || !Number.isSafeInteger(file.revision)
        || file.revision <= 0) return undefined;
    return Object.freeze({ channelId, fileId: file.msgId, revision: file.revision });
}

// dragItemsFor returns the items to move for a drag started on `row`: the whole
// current selection when the row is part of a multi-selection, else just the
// row's own item.
function dragItemsFor(row: HTMLElement, fallback: FileCommandItem): FileCommandItem[] {
    const key = getRowKey(row);
    const selected = state.selectedItems;
    if (key && selected.has(key) && selected.size > 1) return Array.from(selected.values());
    return [fallback];
}

// startDrag begins an internal drag-to-move, resolving multi-select and the set
// of folders that can't be a drop target (a dragged folder or its own subtree).
function startDrag(row: HTMLElement, fallback: FileCommandItem, parentId: string): void {
    // Nothing in the trash has a parent to be dragged out of.
    if (isTrashMode()) return;

    const items = dragItemsFor(row, fallback);
    const folderIds = items
        .filter((item): item is FolderCommandItem => item.type === 'folder')
        .map((item) => item.id);
    beginRowDrag(row, items, parentId, new Set(folderIds));
    if (!folderIds.length) return;

    void refreshFolderIndex()
        .then((index) => {
            if (!state.dragState || state.dragState.row !== row) return;
            const blocked = new Set(folderIds);
            for (const folderId of folderIds) {
                for (const descendant of collectDescendants(folderId, index.children)) blocked.add(descendant);
            }
            state.dragState.blocked = blocked;
        })
        .catch(() => {});
}

// canOwnerActOnFile returns true when the current user is allowed to
// rename/delete the given file. In personal drives it's always true (you
// uploaded everything). In shared drives it's only true when the file's
// recorded uploader matches the current user. Default-deny when uploader
// or self id is unknown.
export function canOwnerActOnFile(file: Pick<FileRowInput, 'uploaderID' | 'uploaderId'> | null | undefined): boolean {
    if (!file) return false;
    if (state.activeChannel?.kind !== 'shared') return true;
    const uploader = Number(file.uploaderID ?? file.uploaderId ?? 0);
    const me = Number(state.myUserID || 0);
    return Boolean(uploader && me && uploader === me);
}

// The last published identity controls both stale request rejection and whether
// a refresh keeps the current grid visible. It intentionally includes the drive
// so two root folders from different drives never share scroll or selection.
let lastRenderedFileView: FileViewIdentity | null = null;
let fileRefreshToken = 0;

function sameFileView(left: FileViewIdentity | null, right: FileViewIdentity): boolean {
    return left?.channelId === right.channelId && left.folderId === right.folderId;
}

export function resetFileListScrollRestore(): void {
    lastRenderedFileView = null;
}


type FileStateKind = "loading" | "empty" | "error";

export function renderFileState(
    list: HTMLElement,
    kind: FileStateKind,
    title: string,
    body = "",
    action?: { label: string; onClick: () => void },
    secondaryAction?: { label: string; onClick: () => void },
) {
    list.removeAttribute('aria-rowcount');
    showFileListState({
        stateKind: kind,
        title,
        body,
        actionLabel: action?.label ?? '',
        onAction: action?.onClick,
        secondaryActionLabel: secondaryAction?.label ?? '',
        onSecondaryAction: secondaryAction?.onClick,
    });
}

function afterFileListPaint(list: HTMLElement, callback: () => void) {
    void tick().then(() => {
        if (!document.body.contains(list)) return;
        callback();
    });
}

export function renderFileListRows(list: HTMLElement, rows: FileListRow[], afterRender?: () => void) {
    list.setAttribute('aria-rowcount', String(rows.length + 1));
    showFileListRows(rows);
    if (afterRender) afterFileListPaint(list, afterRender);
}

// The phone row has no room for the "Name · 2h ago" label, so the chip also
// carries the uploader's initials and first name from the same name cache.
function uploaderChipFor(uploaderID: number, uploadTime: number): FileListUploaderChip | null {
    const label = uploaderChipLabel({ uploaderID, uploadTime });
    if (!label) return null;
    const parts = (state.userNames.get(String(uploaderID)) ?? '').trim().split(/\s+/).filter(Boolean);
    return {
        label,
        firstName: parts[0] ?? '',
        initials: parts.slice(0, 2).map((part) => part[0]).join('').toUpperCase(),
    };
}

function chipForFileRow(row: FileListFileRow) {
    return uploaderChipFor(row.uploaderID, row.uploadTime);
}

export function resolveUploaderChipsForRows(rows: FileListRow[], isCurrent: () => boolean) {
    if (state.activeChannel?.kind !== "shared") return;
    const uploaderIDs = rows
        .filter((row): row is FileListFileRow => row.kind === "file")
        .map((row) => row.uploaderID)
        .filter((id) => Number.isFinite(id) && id > 0);
    if (!uploaderIDs.length) return;

    void ensureUserNames(uploaderIDs).then(() => {
        if (state.activeChannel?.kind !== "shared" || !isCurrent()) return;
        let changed = false;
        updateFileListRows((currentRows) => {
            const nextRows = currentRows.map((row) => {
                if (row.kind !== "file") return row;
                const uploaderChip = chipForFileRow(row);
                if ((row.uploaderChip?.label ?? "") === (uploaderChip?.label ?? "")) return row;
                changed = true;
                return { ...row, uploaderChip };
            });
            return changed ? nextRows : currentRows;
        });
    });
}

function folderAction(): FileListAction {
    return {
        kind: "download",
        className: "download-folder",
        title: "Download",
        label: "Download folder",
    };
}

function fileActions(name: string): FileListAction[] {
    const actions: FileListAction[] = [];
    if (isVideoFile(name || "")) {
        actions.push({
            kind: "play",
            className: "play-video",
            title: "Play",
            label: "Play video",
        });
    } else if (canOpenFileViewer(name || "")) {
        actions.push({
            kind: "open",
            className: "open-file",
            title: "Open",
            label: "Open file",
        });
    }
    actions.push({
        kind: "download",
        className: "download",
        title: "Download",
        label: "Download",
    });
    return actions;
}

export function buildFolderRow(folder: FolderRowInput, parentId: string, overrides: Partial<FolderListRow> = {}): FolderListRow {
    const id = String(folder.id || overrides.id || '');
    const name = String(folder.name || overrides.name || 'Folder');
    return {
        kind: 'folder',
        key: overrides.key || `folder:${id}`,
        selectionKey: overrides.selectionKey || `folder:${id}`,
        id,
        name,
        channelId: Number(overrides.channelId ?? state.activeChannel?.id ?? 0),
        parentId: String(overrides.parentId ?? folder.parentId ?? parentId ?? ''),
        metaLabel: overrides.metaLabel ?? '—',
        sizeLabel: overrides.sizeLabel ?? '…',
        size: overrides.size ?? 0,
        modifiedTime: overrides.modifiedTime ?? 0,
        ariaLabel: overrides.ariaLabel ?? `Folder: ${name}`,
        timeLabel: overrides.timeLabel,
        actionsInline: overrides.actionsInline,
        actions: overrides.actions ?? [folderAction()],
        onClick: overrides.onClick,
        onDoubleClick: overrides.onDoubleClick,
    };
}

export function buildFileRow(file: FileRowInput, parentId: string, overrides: Partial<FileListFileRow> = {}): FileListFileRow {
    const name = String(file.name || overrides.name || 'File');
    const { base, ext } = splitNameAndExt(name);
    const id = String(file.id ?? file.msgId ?? overrides.id ?? '');
    const size = Number(file.size ?? overrides.size ?? 0);
    const uploadTime = Number(file.date ?? file.uploadTime ?? overrides.uploadTime ?? 0);
    const source = file.source === 'tg' || overrides.source === 'tg' ? 'tg' : 'fs';
    const uploaderID = Number(file.uploaderID ?? file.uploaderId ?? overrides.uploaderID ?? 0);
    const encrypted = Boolean(file.encrypted ?? overrides.encrypted ?? false);
    const canDelete = Boolean(file.canDelete ?? overrides.canDelete ?? canOwnerActOnFile(file));
    const canRename = Boolean(file.canRename ?? overrides.canRename ?? canDelete);

    return {
        kind: 'file',
        key: overrides.key || `file:${source}:${id}`,
        selectionKey: overrides.selectionKey || `file:${id}`,
        id,
        name,
        channelId: Number(overrides.channelId ?? state.activeChannel?.id ?? 0),
        baseName: overrides.baseName ?? base,
        ext: overrides.ext ?? ext,
        source,
        parentId: String(overrides.parentId ?? parentId ?? ''),
        size,
        metaLabel: overrides.metaLabel ?? formatDate(uploadTime),
        sizeLabel: overrides.sizeLabel ?? formatBytes(size),
        ariaLabel: overrides.ariaLabel ?? `File: ${name}`,
        timeLabel: overrides.timeLabel,
        actionsInline: overrides.actionsInline,
        uploaderID,
        uploadTime,
        encrypted,
        canDelete,
        canRename,
        thumbnail: overrides.thumbnail,
        uploaderChip: overrides.uploaderChip !== undefined
            ? overrides.uploaderChip
            : uploaderChipFor(uploaderID, uploadTime),
        actions: overrides.actions ?? fileActions(name),
        onClick: overrides.onClick,
        onDoubleClick: overrides.onDoubleClick,
    };
}

function buildPendingFolderRow(tempId: string, name: string): PendingFolderListRow {
    return {
        kind: "pending-folder",
        key: `pending-folder:${tempId}`,
        tempId,
        name,
    };
}

function interactiveRows(list: HTMLElement = document.getElementById("file-list") as HTMLElement) {
    if (!list) return [] as HTMLElement[];
    return Array.from(list.querySelectorAll<HTMLElement>('.drive-row[data-type="folder"], .drive-row[data-type="file"]'));
}

function setFocusedRow(row: HTMLElement | null, { preventScroll = true } = {}) {
    const list = document.getElementById("file-list") as HTMLElement | null;
    if (!list || !row) return;
    setActiveFileRowKey(getRowKey(row));
    void tick().then(() => {
        if (!document.body.contains(row)) return;
        row.focus({ preventScroll });
    });
}

function rowForSelectionKey(list: HTMLElement, key: string): HTMLElement | null {
    return interactiveRows(list).find((row) => getRowKey(row) === key) ?? null;
}

function setFocusedLogicalRow(key: string, { preventScroll = false } = {}): void {
    const list = document.getElementById('file-list') as HTMLElement | null;
    if (!list || !key) return;
    setActiveFileRowKey(key);
    window.dispatchEvent(new CustomEvent('tdrive:reveal-file-row', { detail: { key } }));
    void tick().then(() => {
        requestAnimationFrame(() => {
            const row = rowForSelectionKey(list, key);
            row?.focus({ preventScroll });
        });
    });
}

export function syncDriveRowTabStops(list: HTMLElement, preferred?: HTMLElement | null) {
    const rows = interactiveRows(list);
    if (!rows.length) {
        setActiveFileRowKey("");
        return;
    }
    const current = list.querySelector<HTMLElement>('.drive-row[tabindex="0"]');
    const target = preferred && rows.includes(preferred)
        ? preferred
        : current && rows.includes(current)
            ? current
            : rows[0];
    setActiveFileRowKey(getRowKey(target));
}

function activeRowFromEventTarget(target: EventTarget | null) {
    const row = (target as HTMLElement | null)?.closest?.('.drive-row[data-type="folder"], .drive-row[data-type="file"]') as HTMLElement | null;
    if (row) return row;
    const list = document.getElementById("file-list") as HTMLElement | null;
    return (document.activeElement as HTMLElement | null)?.closest?.('.drive-row[data-type="folder"], .drive-row[data-type="file"]') as HTMLElement | null
        || list?.querySelector<HTMLElement>('.drive-row[tabindex="0"]')
        || list?.querySelector<HTMLElement>('.drive-row[data-type="folder"], .drive-row[data-type="file"]')
        || null;
}

function triggerRowContextMenu(row: HTMLElement) {
    const rect = row.getBoundingClientRect();
    row.dispatchEvent(new MouseEvent("contextmenu", {
        bubbles: true,
        cancelable: true,
        clientX: rect.left + Math.min(48, rect.width / 2),
        clientY: rect.top + Math.min(24, rect.height / 2),
    }));
}

/** The folder path an item sits in, as the sheet's Location line shows it. */
function rowLocationLabel(): string {
    const trail = get(breadcrumbPath);
    if (!trail.length) return state.activeChannel?.title || 'Drive';
    return `/${trail.map((entry) => entry.name).join('/')}`;
}

/** The Type / Size / Added / Location lines under the sheet's actions. */
function rowDetailLines(row: FolderListRow | FileListFileRow): ContextMenuDetail[] {
    const added = row.kind === 'folder' ? row.modifiedTime : row.uploadTime;
    const type = row.kind === 'folder'
        ? 'Folder'
        : row.ext ? `${row.ext.toUpperCase()} file` : 'File';
    const lines: ContextMenuDetail[] = [{ label: 'Type', value: type, icon: 'type' }];
    // A folder's size is an async subtree lookup that can still be zero, and a
    // blank line reads better than claiming the folder holds nothing.
    if (row.size > 0) lines.push({ label: 'Size', value: formatBytes(row.size), icon: 'size' });
    if (added > 0) {
        lines.push({ label: row.kind === 'folder' ? 'Updated' : 'Added', value: formatDate(added), icon: 'added' });
    }
    lines.push({ label: 'Location', value: rowLocationLabel(), icon: 'location' });
    return lines;
}

// A long press or the overflow button opens the item's own action sheet. It
// never selects the row: on a phone the selection belongs to the reader, not
// the menu, and the sheet header names what the actions apply to.
function openRowMenu(row: HTMLElement, clientX: number, clientY: number): void {
    // Every item on the row menu (open, rename, move, download, delete) acts on
    // a live file. The trash offers its two real actions on the row itself.
    if (isTrashMode()) return;

    const logical = fileListRowForElement(row);
    showRowContextMenu(row, clientX, clientY, {
        header: logical
            ? {
                title: logical.name,
                meta: rowMetaLine(logical),
                kind: logical.kind,
                ext: logical.kind === 'file' ? logical.ext : undefined,
                details: rowDetailLines(logical),
            }
            : undefined,
    });
}

function toggleRowSelection(row: HTMLElement): void {
    // The selection bar acts on live items -- move, download, delete. None of
    // those mean anything for something already deleted, and the two that do
    // are each row's own buttons, so the trash does not select at all.
    if (isTrashMode()) return;
    if (isRowSelected(row)) {
        deselectRow(row);
        return;
    }
    const index = getInteractiveFileListRows().findIndex((candidate) => candidate.selectionKey === getRowKey(row));
    selectRow(row, index);
}

// The phone previews an image in the context of its folder, so swiping moves
// through the other images here in list order.
async function openImagePreview(row: FileListFileRow): Promise<void> {
    const images = getInteractiveFileListRows()
        .filter((candidate): candidate is FileListFileRow => candidate.kind === 'file' && isImageFile(candidate.name))
        .map((image) => ({
            type: 'file',
            id: Number(image.id),
            name: image.name,
            size: image.size,
            encrypted: image.encrypted,
            uploaderId: image.uploaderID,
            uploadTime: image.uploadTime,
        }));
    const index = Math.max(0, images.findIndex((image) => String(image.id) === row.id));
    const preview = await import('./modals/preview');
    preview.activatePreviewModal();
    await preview.openPreviewList(images, index);
}

/**
 * The command payload for a file row. Spelled out per source because the
 * command item is a discriminated union: a Telegram file has to carry the
 * folder it came from, a projected one does not.
 */
function fileCommandFor(row: FileListFileRow, parentId: string): FileCommandItem {
    const target = { type: 'file' as const, id: Number(row.id), name: row.name, size: row.size, parentId };
    return row.source === 'tg' ? { ...target, source: 'tg' } : { ...target, source: 'fs' };
}

function deleteRow(row: InteractiveFileListRow) {
    if (isTrashMode()) return;

    if (row.kind === "folder") {
        openDeleteModal({
            type: "folder",
            id: row.id,
            name: row.name,
            parentId: row.parentId || state.currentFolderId,
        });
        return;
    }
    if (!row.canDelete) return;
    openDeleteModal({
        ...fileCommandFor(row, row.parentId || state.currentFolderId),
        canDelete: row.canDelete,
    });
}

function renameRow(row: InteractiveFileListRow) {
    if (isTrashMode()) return;

    if (row.kind === "folder") {
        openRenameModal({
            type: "folder",
            id: row.id,
            name: row.name,
            parentId: row.parentId || state.currentFolderId,
        });
        return;
    }
    if (!row.canRename) return;
    openRenameModal(fileCommandFor(row, row.parentId || state.currentFolderId));
}

/**
 * Opens the move picker for one row, whichever kind it is. Used by the swipe
 * action, which shows a single verb and so must resolve the target itself
 * rather than leaning on the menu's branching.
 */
function openMoveForRow(row: InteractiveFileListRow): void {
    if (isTrashMode()) return;

    if (row.kind === 'folder') {
        openMoveModal({ type: 'folder', id: row.id, name: row.name, parentId: state.currentFolderId });
        return;
    }
    // A search result sits in its own folder, not the one on screen, so the
    // picker opens where the file actually lives -- including the root, which
    // is an empty string and must not fall back to the current folder.
    openMoveModal(fileCommandFor(row, row.parentId));
}

function fileTargetForRow(row: FileListFileRow) {
    return {
        id: Number(row.id),
        // Captured when the row was rendered, so an action taken after a drive
        // switch still reads from the drive the reader was looking at.
        channelId: row.channelId ?? Number(state.activeChannel?.id),
        name: row.name,
        size: row.size,
        encrypted: row.encrypted,
    };
}

function activateRow(element: HTMLElement, row: InteractiveFileListRow) {
    if (isTrashMode()) return;
    if (isSearchMode()) {
        element.dispatchEvent(new MouseEvent("dblclick", { bubbles: true, cancelable: true }));
        return;
    }
    if (row.kind === "folder") {
        navigateToFolder(row.id, row.name);
        return;
    }
    const target = fileTargetForRow(row);
    if (isVideoFile(target.name)) {
        void appActions().playVideo(target);
        return;
    }
    if (isMobilePlatform() && isImageFile(target.name)) {
        void openImagePreview(row);
        return;
    }
    if (canOpenFileViewer(target.name)) {
        void appActions().openFile(target);
        return;
    }
    enqueueDownload(target.id, target.name, target.size, target.channelId);
}


function isCurrentFileRequest(request: FileRefreshRequest): boolean {
    return (
        request.token === fileRefreshToken
        && state.virtualView === null
        && Number(state.activeChannel?.id ?? 0) === request.view.channelId
        && state.currentFolderId === request.view.folderId
        && state.folderSizeEpoch === request.folderEpoch
    );
}

function refreshStorageUsage(request: FileRefreshRequest, storageUsed: HTMLElement | null): void {
    if (!storageUsed) return;
    if (request.presentation === 'foreground-navigation') {
        storageUsed.innerText = 'Calculating... / Unlimited';
    }
    void getStorageUsed()
        .then((bytes) => {
            if (!isCurrentFileRequest(request)) return;
            const value = Number(bytes);
            storageUsed.innerText = Number.isFinite(value) && value >= 0
                ? `${formatBytes(value)} / Unlimited`
                : '— / Unlimited';
        })
        .catch(() => {
            if (isCurrentFileRequest(request)) storageUsed.innerText = '— / Unlimited';
        });
}

async function loadFileData(view: FileViewIdentity): Promise<LoadedFileData> {
    const contents = await apiGetFolderContents(view.folderId);
    const filesystemFiles = contents.files;
    const [folderStats, telegramFiles] = await Promise.all([
        contents.folders.length > 0
            ? calculateVisibleFolderStats(view.folderId).catch(() => new Map<string, FolderStat>())
            : Promise.resolve(new Map<string, FolderStat>()),
        view.folderId === '' ? getFileList() : Promise.resolve([] as RootFile[]),
    ]);
    const normalizedTelegramFiles = Array.isArray(telegramFiles) ? telegramFiles : [];
    let filesystemMessageIds = new Set(filesystemFiles.map((file) => file.msgId));

    if (view.folderId === '' && normalizedTelegramFiles.length > 0) {
        try {
            filesystemMessageIds = new Set(await getAllFsMsgIds());
        } catch (error) {
            // The visible folder remains correct with its direct filesystem IDs.
            console.warn('GetAllFsMsgIDs failed:', error);
        }
    }

    return {
        folders: contents.folders,
        filesystemFiles,
        telegramFiles: normalizedTelegramFiles,
        filesystemMessageIds,
        folderStats,
    };
}

function rowsForLoadedData(data: LoadedFileData, view: FileViewIdentity): FileListRow[] {
    const pendingRows: PendingFolderListRow[] = [];
    for (const [tempId, operation] of state.pendingFolderOps) {
        if (operation.parentId === view.folderId) {
            pendingRows.push(buildPendingFolderRow(tempId, operation.name));
        }
    }

    const folderRows = [...data.folders]
        .sort((left, right) => left.name.localeCompare(right.name))
        .map((folder) => {
            const stats = data.folderStats.get(folder.id);
            const size = stats?.bytes ?? 0;
            const modifiedTime = stats?.latestUpload ?? 0;
            return buildFolderRow(folder, view.folderId, {
                size,
                modifiedTime,
                sizeLabel: formatBytes(size),
                metaLabel: modifiedTime > 0 ? formatDate(modifiedTime) : '—',
            });
        });

    const filesystemRows = data.filesystemFiles.map((file) => buildFileRow({
        source: 'fs',
        id: file.msgId,
        name: file.name,
        size: file.encrypted && file.plaintextSize > 0 ? file.plaintextSize : file.size,
        date: file.uploadTime,
        uploaderID: file.uploaderId,
        encrypted: file.encrypted,
    }, view.folderId, {
        // Only the current folder's projected images opt in. This avoids a
        // gallery-wide lookup and keeps raw Telegram/search rows icon-only.
        thumbnail: fileThumbnailIdentity(file, view.channelId),
    }));
    const telegramRows = data.telegramFiles
        .filter((file) => !data.filesystemMessageIds.has(file.msgId))
        .map((file) => buildFileRow({
            source: 'tg',
            id: file.msgId,
            name: file.name,
            size: file.size,
            date: file.date,
        }, view.folderId));
    const fileRows = [...filesystemRows, ...telegramRows]
        .sort((left, right) => right.uploadTime - left.uploadTime);

    return [...pendingRows, ...folderRows, ...fileRows];
}

function applyPendingFocus(list: HTMLElement): void {
    if (state.pendingFocus?.type !== 'file') return;
    const targetId = String(state.pendingFocus.id || '');
    const logicalRows = getInteractiveFileListRows();
    const targetRow = targetId
        ? logicalRows.find((row): row is FileListFileRow => row.kind === 'file' && row.id === targetId)
        : undefined;
    // A virtual window does not mount every row. Do not consume navigation
    // intent until the target exists logically and has actually mounted.
    if (!targetRow) return;

    setActiveFileRowKey(targetRow.selectionKey);
    window.dispatchEvent(new CustomEvent('tdrive:reveal-file-row', { detail: { key: targetRow.selectionKey } }));
    void tick().then(() => {
        requestAnimationFrame(() => {
            if (state.pendingFocus?.type !== 'file' || String(state.pendingFocus.id || '') !== targetId) return;
            const target = rowForSelectionKey(list, targetRow.selectionKey);
            if (!target) return;
            const index = getInteractiveFileListRows()
                .findIndex((row) => row.selectionKey === targetRow.selectionKey);
            if (index < 0) return;
            clearSelection();
            selectRow(target, index);
            setFocusedRow(target, { preventScroll: true });
            state.pendingFocus = null;
        });
    });
}

function publishLoadedFileData(list: HTMLElement, request: FileRefreshRequest, data: LoadedFileData): void {
    if (!isCurrentFileRequest(request)) return;
    const rows = rowsForLoadedData(data, request.view);
    const preserveScroll = sameFileView(lastRenderedFileView, request.view);
    const scrollTop = preserveScroll ? list.scrollTop : 0;

    if (request.view.folderId === '') {
        state.telegramRootCache = data.telegramFiles;
        state.telegramRootCacheDriveKey = String(request.view.channelId);
    }
    lastRenderedFileView = request.view;

    const afterPublish = () => {
        if (!isCurrentFileRequest(request)) return;
        reconcileSelection(list, getInteractiveFileListRows());
        syncDriveRowTabStops(list);
        if (preserveScroll) list.scrollTop = scrollTop;
        applyPendingFocus(list);
        // Opening a folder unmounts the row that had the keyboard, and with
        // it the keyboard: focus fell to <body> and the arrows went dead.
        // Only a view change, and only when nothing else has taken focus.
        if (!preserveScroll && state.pendingFocus === null && document.activeElement === document.body && !isMobilePlatform()) {
            list.focus({ preventScroll: true });
        }
        resolveUploaderChipsForRows(rows, () => isCurrentFileRequest(request));
    };

    if (rows.length === 0) {
        // An empty folder is the one place a reader is certain to want the
        // upload picker, so it is offered here instead of only in the toolbar.
        if (isMobilePlatform()) {
            renderFileState(
                list,
                'empty',
                'No files yet.',
                '',
                { label: 'Upload files', onClick: () => chooseFilesForCurrentFolder() },
                { label: 'Create folder', onClick: openNewFolderModal },
            );
        } else {
            renderFileState(
                list,
                'empty',
                'This folder is empty',
                'Upload files or create a folder to start organizing this drive.',
                { label: 'Upload files', onClick: () => chooseFilesForCurrentFolder() },
            );
        }
        afterFileListPaint(list, afterPublish);
        return;
    }

    renderFileListRows(list, rows, afterPublish);
}

function publishRefreshError(list: HTMLElement, request: FileRefreshRequest, error: unknown): void {
    if (!isCurrentFileRequest(request)) return;
    if (request.presentation === 'same-view-refresh') {
        console.warn('Same-view file refresh failed:', error);
        return;
    }
    // A dead link is not a folder problem, and saying so saves the reader from
    // hunting for a fault that is not there.
    const offline = isOffline();
    renderFileState(
        list,
        'error',
        offline ? "You're offline" : 'Could not load this folder',
        offline ? 'Reconnect to load this folder from Telegram.' : humanizeBackendError(error),
        { label: 'Retry', onClick: () => appActions().refreshFiles() },
    );
}

export function refreshFiles({ background = false }: RefreshFilesOptions = {}): void {
    // Both modes are set from the one value, before anything branches. They
    // used to be cleared on the way past each other, so entering Photos from
    // the trash returned before the line that turned the trash off: the drive
    // chrome kept the trash's title and left both sidebar entries lit.
    const destination = state.virtualView;
    setPhotosMode(destination === 'photos');
    setTrashMode(destination === 'trash');
    if (destination === 'photos') {
        // A request that began before Photos is never permitted to republish
        // over the gallery or the preserved drive list on return.
        fileRefreshToken += 1;
        clearSelection();
        void renderGallery({ background });
        return;
    }
    if (destination === 'trash') {
        // Same reasoning as Photos: invalidate any drive load already in
        // flight so it cannot republish a folder over the trash.
        fileRefreshToken += 1;
        clearSelection();
        renderTrashRows();
        return;
    }

    const list = document.getElementById('file-list');
    if (!list) return;
    const view: FileViewIdentity = {
        channelId: Number(state.activeChannel?.id ?? 0),
        folderId: state.currentFolderId,
    };
    const presentation: FileRefreshPresentation = background || sameFileView(lastRenderedFileView, view)
        ? 'same-view-refresh'
        : 'foreground-navigation';

    resetFolderCaches();
    const request: FileRefreshRequest = {
        token: ++fileRefreshToken,
        view,
        presentation,
        folderEpoch: state.folderSizeEpoch,
    };

    if (presentation === 'foreground-navigation') {
        clearSelection();
        renderFileState(list, 'loading', 'Loading files');
    }
    refreshStorageUsage(request, document.getElementById('storage-used'));

    void loadFileData(view)
        .then((data) => publishLoadedFileData(list, request, data))
        .catch((error: unknown) => publishRefreshError(list, request, error));
}

// Delegated row interactions. Listeners live on the #file-list container and
// resolve each event back to its published row by key, so re-rendering rows
// costs no listener churn and Svelte can freely replace keyed rows.
function isSearchMode() {
    return String(state.searchQuery || "").trim() !== "";
}

// In the trash a row is a record of something deleted, not a live item: it
// cannot be opened, renamed, moved, dragged or downloaded. The two things it
// can do are its own buttons, so every drive interaction below checks this
// first rather than each one deciding for itself.
function isTrashMode() {
    return state.virtualView === 'trash';
}

// On a phone a tap opens the row (the desktop double click) unless something is
// already selected, when it toggles the row instead, and the trailing button
// opens the row's menu.
function handleMobileRowAction(e: MouseEvent, row: HTMLElement): boolean {
    const target = e.target as HTMLElement;
    // The revealed swipe button commits and stops there: it must not also run
    // the tap that would have opened the row underneath it.
    const swipeAction = target.closest<HTMLButtonElement>('button[data-swipe-action]');
    if (swipeAction) {
        e.stopPropagation();
        const swiped = fileListRowForElement(swipeAction);
        if (swiped) openMoveForRow(swiped);
        return true;
    }

    const more = target.closest<HTMLButtonElement>('button.row-more');
    if (more) {
        const rect = more.getBoundingClientRect();
        openRowMenu(row, rect.left + rect.width / 2, rect.bottom);
        // This click is fully spent on opening the sheet. Left to bubble, it
        // reaches the menu's own dismiss-on-outside-click handler on document,
        // which sees a target outside a sheet that has not rendered yet and
        // closes it again in the same tick. Long-press escaped that only
        // because it fires on a timer with no click of its own.
        e.stopPropagation();
        return true;
    }
    if (target.closest('button')) return true;
    return false;
}

function handleMobileTap(e: MouseEvent, element: HTMLElement, row: InteractiveFileListRow) {
    if (handleMobileRowAction(e, element)) return;
    setFocusedRow(element, { preventScroll: true });
    if (state.selectedItems.size > 0) {
        toggleRowSelection(element);
        return;
    }
    activateRow(element, row);
}

function handleListClick(e: MouseEvent) {
    const element = (e.target as HTMLElement).closest(".drive-row") as HTMLElement | null;
    // A row still being created, or one the list has already dropped, has
    // nothing to act on.
    const row = fileListRowForElement(element);
    if (!element || !row) return;

    // A trash row's only two operations are its own buttons, which Svelte binds
    // directly. Everything this delegated layer would otherwise do -- select,
    // focus, open -- has no meaning for a deleted item, so it stops here.
    if (isTrashMode()) return;

    // Search owns row activation so it can enter a result's folder before
    // refreshing. The mobile action affordances still belong to this shared
    // delegated layer, otherwise its overflow button is a dead end.
    if (isSearchMode()) {
        if (isMobilePlatform()) handleMobileRowAction(e, element);
        return;
    }

    if (isMobilePlatform()) {
        handleMobileTap(e, element, row);
        return;
    }

    if (row.kind === "folder") {
        if ((e.target as HTMLElement).closest("button.download-folder")) {
            enqueueFolderDownload(row.id, row.name, 0, row.channelId);
            return;
        }
        setFocusedRow(element, { preventScroll: true });
        handleRowSelection(element, e, getInteractiveFileListRows());
        return;
    }

    const target = fileTargetForRow(row);
    if ((e.target as HTMLElement).closest("button.download")) {
        enqueueDownload(target.id, target.name, target.size, target.channelId);
        return;
    }
    if ((e.target as HTMLElement).closest("button.play-video")) {
        void appActions().playVideo(target);
        return;
    }
    if ((e.target as HTMLElement).closest("button.open-file")) {
        void appActions().openFile(target);
        return;
    }
    if ((e.target as HTMLElement).closest("button")) return;
    setFocusedRow(element, { preventScroll: true });
    handleRowSelection(element, e, getInteractiveFileListRows());
}

function handleListKeyDown(e: KeyboardEvent) {
    const target = e.target as HTMLElement | null;
    const action = target?.closest<HTMLButtonElement>('.row-actions button');
    if (action) {
        if (e.key !== 'ArrowLeft' && e.key !== 'Escape') return;
        const row = action.closest<HTMLElement>('.drive-row');
        if (!row) return;
        e.preventDefault();
        setFocusedRow(row, { preventScroll: true });
        return;
    }
    if (target?.closest("input, textarea, select, [contenteditable='true']")) return;

    const list = document.getElementById('file-list') as HTMLElement | null;
    if (!list) return;
    const renderedRows = interactiveRows(list);
    const logicalRows = getInteractiveFileListRows();
    if (!renderedRows.length || !logicalRows.length) return;

    const current = activeRowFromEventTarget(e.target) || renderedRows[0];
    const currentRow = fileListRowForElement(current);
    const currentIndex = Math.max(0, logicalRows.findIndex((row) => row.selectionKey === currentRow?.selectionKey));
    let nextKey: string | null = null;

    switch (e.key) {
        case 'ArrowDown':
            nextKey = logicalRows[Math.min(logicalRows.length - 1, currentIndex + 1)]?.selectionKey ?? null;
            break;
        case 'ArrowUp':
            nextKey = logicalRows[Math.max(0, currentIndex - 1)]?.selectionKey ?? null;
            break;
        case 'Home':
            nextKey = logicalRows[0]?.selectionKey ?? null;
            break;
        case 'End':
            nextKey = logicalRows[logicalRows.length - 1]?.selectionKey ?? null;
            break;
        case 'ArrowRight': {
            const firstAction = current.querySelector<HTMLButtonElement>('.row-actions button:not(:disabled)');
            if (!firstAction) return;
            e.preventDefault();
            firstAction.focus();
            return;
        }
        case ' ':
            e.preventDefault();
            handleRowSelection(current, e, logicalRows);
            return;
        case 'Enter':
            e.preventDefault();
            if (currentRow) activateRow(current, currentRow);
            return;
        case 'F2':
            e.preventDefault();
            if (currentRow) renameRow(currentRow);
            return;
        case 'Delete':
        case 'Backspace':
            // A Mac keyboard's Delete key is Backspace.
            e.preventDefault();
            if (currentRow) deleteRow(currentRow);
            return;
        case 'ContextMenu':
            e.preventDefault();
            triggerRowContextMenu(current);
            return;
        case 'F10':
            if (!e.shiftKey) return;
            e.preventDefault();
            triggerRowContextMenu(current);
            return;
        default:
            return;
    }

    if (!nextKey) return;
    e.preventDefault();
    setFocusedLogicalRow(nextKey, { preventScroll: false });
}

function handleListDblClick(e: MouseEvent) {
    // A phone tap already opened the row; the second tap of a quick pair is not
    // a rename request.
    if (isTrashMode() || isSearchMode() || isMobilePlatform()) return;

    const row = fileListRowForElement((e.target as HTMLElement).closest(".drive-row"));
    if (!row) return;

    if (row.kind === "folder") {
        navigateToFolder(row.id, row.name);
        return;
    }

    // Rename only from the name area and only when allowed.
    if (!(e.target as HTMLElement).closest(".row-name")) return;
    const target = fileTargetForRow(row);
    if (isVideoFile(target.name)) {
        e.preventDefault();
        window.getSelection?.()?.removeAllRanges();
        void appActions().playVideo(target);
        return;
    }
    if (canOpenFileViewer(target.name)) {
        e.preventDefault();
        window.getSelection?.()?.removeAllRanges();
        void appActions().openFile(target);
        return;
    }
    if (!row.canRename) return;
    e.preventDefault();
    const selection = window.getSelection?.();
    if (selection) selection.removeAllRanges();
    openRenameModal(fileCommandFor(row, state.currentFolderId));
}

function handleListDragStart(e: DragEvent) {
    const handle = (e.target as HTMLElement | null)?.closest?.(".row-name[draggable='true']") as HTMLElement | null;
    const element = handle?.closest(".drive-row") as HTMLElement | null;
    const row = fileListRowForElement(element);
    if (!element || !row) return;

    const selection = window.getSelection?.();
    if (selection) selection.removeAllRanges();

    if (e.dataTransfer) {
        e.dataTransfer.effectAllowed = "move";
        try {
            e.dataTransfer.setData("text/plain", "tdrive-move");
        } catch {}
    }

    const parentId = row.parentId || state.currentFolderId;
    if (row.kind === "folder") {
        startDrag(element, { type: "folder", id: row.id, name: row.name, parentId, row: element }, parentId);
        return;
    }
    startDrag(element, { ...fileCommandFor(row, parentId), row: element }, parentId);
}

/**
 * The folder a drag event is over, or null for anything else. A drop target has
 * to be a folder the list is currently showing: a row left behind by the last
 * render would name a folder the move could not land in.
 */
function dropTargetFolder(target: EventTarget | null): { element: HTMLElement; folder: FolderListRow } | null {
    const element = (target as HTMLElement | null)?.closest?.(".drive-row") as HTMLElement | null;
    const row = fileListRowForElement(element);
    return element && row?.kind === 'folder' ? { element, folder: row } : null;
}

function handleListDragOver(e: DragEvent) {
    if (!state.dragState) return;
    const over = dropTargetFolder(e.target);
    if (!over) return;
    const allowed = canDropOnFolder(over.folder.id);
    setDropHighlight(over.element, allowed);
    if (e.dataTransfer) e.dataTransfer.dropEffect = allowed ? "move" : "none";
    if (allowed) e.preventDefault();
}

function handleListDragLeave(e: DragEvent) {
    const over = dropTargetFolder(e.target);
    if (!over) return;
    if (e.relatedTarget && over.element.contains(e.relatedTarget as Node)) return;
    if (state.dragOverEl === over.element) {
        over.element.classList.remove("drop-target");
        over.element.classList.remove("drop-denied");
        state.dragOverEl = null;
    }
}

async function handleListDrop(e: DragEvent) {
    if (!state.dragState) return;
    const over = dropTargetFolder(e.target);
    if (!over || !canDropOnFolder(over.folder.id)) return;
    e.preventDefault();
    e.stopPropagation();
    if (state.dragOverEl === over.element) state.dragOverEl = null;
    over.element.classList.remove("drop-target");
    over.element.classList.remove("drop-denied");
    await performDropMove(over.folder.id);
}

export function activateFileList(): () => void {
    const list = document.getElementById('file-list');
    if (!list) return () => {};

    const onDrop = (event: DragEvent) => {
        void handleListDrop(event);
    };

    list.addEventListener('click', handleListClick);
    list.addEventListener('dblclick', handleListDblClick);
    list.addEventListener('keydown', handleListKeyDown);
    list.addEventListener('dragstart', handleListDragStart);
    list.addEventListener('dragend', endRowDrag);
    list.addEventListener('dragover', handleListDragOver);
    list.addEventListener('dragleave', handleListDragLeave);
    list.addEventListener('drop', onDrop);

    // Phone gestures: a long press selects the row, and a pull from the top
    // refreshes.
    //
    // The press used to select only when it landed on the row's small leading
    // icon and to open the menu anywhere else. Nobody found it: holding a row
    // means "select this" on every phone anyone has used, and a target that
    // narrow is not a gesture, it is a secret. The menu lost nothing by giving
    // the press up, because every row already carries its own button for it.
    const touchCleanups = isMobilePlatform()
        ? [
            bindLongPress(list, '.drive-row[data-type="folder"], .drive-row[data-type="file"]', (row) => {
                toggleRowSelection(row);
            }),
            bindPullToRefresh(list, () => appActions().triggerRefresh()),
            bindSwipeActions(list, '.drive-row[data-type="folder"], .drive-row[data-type="file"]', {
                openWidth: (row) => row.querySelector<HTMLElement>('.row-swipe-actions')?.offsetWidth ?? 0,
                // The class is what the row's own styling keys off; the binder
                // owns the transform, so nothing else has to know the width.
                onSettle: (row, open) => row.classList.toggle('is-swiped', open),
            }),
        ]
        : [];
    return () => {
        for (const cleanup of touchCleanups) cleanup();
        list.removeEventListener('click', handleListClick);
        list.removeEventListener('dblclick', handleListDblClick);
        list.removeEventListener('keydown', handleListKeyDown);
        list.removeEventListener('dragstart', handleListDragStart);
        list.removeEventListener('dragend', endRowDrag);
        list.removeEventListener('dragover', handleListDragOver);
        list.removeEventListener('dragleave', handleListDragLeave);
        list.removeEventListener('drop', onDrop);
    };
}
