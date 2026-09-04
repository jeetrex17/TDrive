// File list rendering for TDrive frontend

import { state, resetFolderCaches } from '../state';
import { splitNameAndExt, formatDate, formatBytes } from '../utils';
import { tick } from 'svelte';
import { clearSelection, handleRowSelection, selectRow, getRowKey } from './selection';
import { openRenameModal } from './modals/rename';
import { openDeleteModal } from './modals/delete';
import { navigateToFolder } from './navigation';
import { beginRowDrag, endRowDrag, canDropOnFolder, setDropHighlight, performDropMove } from './drag-drop';
import {
    GetFileList, GetStorageUsed,
} from '../../wailsjs/go/main/App';
import { getFolderContents as apiGetFolderContents } from '../api';
import { calculateVisibleFolderStats, getAllFsMsgIDs, type FolderStats } from './drive-data';
import { refreshFolderIndex, collectDescendants } from './folder-index';
import { enqueueDownload, enqueueFolderDownload } from './transfers';
import { ensureUserNames, uploaderChipLabel } from './uploaders';
import { renderGallery, setPhotosMode } from './gallery';
import { isVideoFile } from './media-types';
import { openVideoModal } from './modals/video';
import { canOpenFileViewer, openFileViewer } from './modals/file-viewer';
import FileList from '../ui/file-list/FileList.svelte';
import { showFileListRows, showFileListState, updateFileListRows } from '../ui/file-list/file-list-store';
import { setActiveFileRowKey } from '../ui/file-list/row-state-store';
import type { FileListAction, FileListFileRow, FileListRow, FolderListRow, PendingFolderListRow } from '../ui/file-list/types';
import { mountSvelte, type SvelteMountHandle } from '../ui';

// dragItemsFor returns the items to move for a drag started on `row`: the whole
// current selection when the row is part of a multi-selection, else just the
// row's own item.
function dragItemsFor(row: HTMLElement, fallback: any): any[] {
    const key = getRowKey(row);
    const sel = state.selectedItems;
    if (key && sel.has(key) && sel.size > 1) {
        return Array.from(sel.values());
    }
    return [fallback];
}

// startDrag begins an internal drag-to-move, resolving multi-select and the set
// of folders that can't be a drop target (a dragged folder or its own subtree).
function startDrag(row: HTMLElement, fallback: any, parentId: string) {
    const items = dragItemsFor(row, fallback);
    const folderIds = items
        .filter((i: any) => i && i.type === "folder")
        .map((i: any) => String(i.id));
    beginRowDrag(row, items, parentId, new Set<string>(folderIds));
    if (folderIds.length) {
        refreshFolderIndex()
            .then((index: any) => {
                if (!state.dragState || state.dragState.row !== row) return;
                const blocked = new Set<string>(folderIds);
                for (const fid of folderIds) {
                    for (const d of collectDescendants(fid, index.children)) blocked.add(d);
                }
                state.dragState.blocked = blocked;
            })
            .catch(() => {});
    }
}

// canOwnerActOnFile returns true when the current user is allowed to
// rename/delete the given file. In personal drives it's always true (you
// uploaded everything). In shared drives it's only true when the file's
// recorded uploader matches the current user. Default-deny when uploader
// or self id is unknown.
export function canOwnerActOnFile(file: any) {
    if (!file) return false;
    if (state.activeChannel?.kind !== "shared") return true;
    const uploader = Number(file.uploaderID ?? file.uploader_id ?? 0);
    const me = Number(state.myUserID || 0);
    if (!uploader || !me) return false;
    return uploader === me;
}

// Tracks the folder whose rows are currently rendered, so a same-folder
// re-render can restore the scroll position instead of jumping to the top.
let lastRenderedFolderId: string | null = null;
let fileListHandle: SvelteMountHandle<Record<string, unknown>> | null = null;

export function resetFileListScrollRestore() {
    lastRenderedFolderId = null;
}

function ensureFileListView(list: HTMLElement) {
    if (fileListHandle) return;
    list.replaceChildren();
    fileListHandle = mountSvelte(FileList, {
        target: list,
        props: {},
    });
}

type FileStateKind = "loading" | "empty" | "error";

export function renderFileState(
    list: HTMLElement,
    kind: FileStateKind,
    title: string,
    body = "",
    action?: { label: string; onClick: () => void },
) {
    ensureFileListView(list);
    showFileListState({
        stateKind: kind,
        title,
        body,
        actionLabel: action?.label ?? '',
        onAction: action?.onClick,
    });
}

function afterFileListPaint(list: HTMLElement, callback: () => void) {
    void tick().then(() => {
        if (!document.body.contains(list)) return;
        callback();
    });
}

export function renderFileListRows(list: HTMLElement, rows: FileListRow[], afterRender?: () => void) {
    ensureFileListView(list);
    showFileListRows(rows);
    if (afterRender) afterFileListPaint(list, afterRender);
}

function chipForFileRow(row: FileListFileRow) {
    const label = uploaderChipLabel({
        uploaderID: row.uploaderID,
        uploadTime: row.uploadTime,
    });
    return label ? { label } : null;
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

export function buildFolderRow(folder: any, parentId: string, overrides: Partial<FolderListRow> = {}): FolderListRow {
    const id = String(folder?.id || overrides.id || "");
    const name = String(folder?.name || overrides.name || "Folder");
    return {
        kind: "folder",
        key: overrides.key || `folder:${id}`,
        selectionKey: overrides.selectionKey || `folder:${id}`,
        id,
        name,
        parentId: String(overrides.parentId ?? parentId ?? ""),
        metaLabel: overrides.metaLabel ?? "—",
        sizeLabel: overrides.sizeLabel ?? "…",
        size: overrides.size ?? 0,
        modifiedTime: overrides.modifiedTime ?? 0,
        ariaLabel: overrides.ariaLabel ?? `Folder: ${name}`,
        actions: overrides.actions ?? [folderAction()],
        onClick: overrides.onClick,
        onDoubleClick: overrides.onDoubleClick,
    };
}

export function buildFileRow(file: any, parentId: string, overrides: Partial<FileListFileRow> = {}): FileListFileRow {
    const name = String(file?.name || overrides.name || "File");
    const { base, ext } = splitNameAndExt(name);
    const id = String(file?.id ?? overrides.id ?? "");
    const size = Number(file?.size ?? overrides.size ?? 0);
    const uploadTime = Number(file?.date ?? file?.uploadTime ?? overrides.uploadTime ?? 0);
    const source = String(file?.source ?? overrides.source ?? "fs");
    const uploaderID = Number(file?.uploaderID ?? file?.uploaderId ?? overrides.uploaderID ?? 0);
    const encrypted = Boolean(file?.encrypted ?? overrides.encrypted ?? false);
    const canDelete = Boolean(file?.canDelete ?? overrides.canDelete ?? canOwnerActOnFile(file));
    const canRename = Boolean(file?.canRename ?? overrides.canRename ?? canDelete);
    return {
        kind: "file",
        key: overrides.key || `file:${source}:${id}`,
        selectionKey: overrides.selectionKey || `file:${id}`,
        id,
        name,
        baseName: overrides.baseName ?? base,
        ext: overrides.ext ?? ext,
        source,
        parentId: String(overrides.parentId ?? parentId ?? ""),
        size,
        metaLabel: overrides.metaLabel ?? formatDate(uploadTime),
        sizeLabel: overrides.sizeLabel ?? formatBytes(size),
        ariaLabel: overrides.ariaLabel ?? `File: ${name}`,
        uploaderID,
        uploadTime,
        encrypted,
        canDelete,
        canRename,
        uploaderChip: overrides.uploaderChip !== undefined
            ? overrides.uploaderChip
            : (() => {
                const label = uploaderChipLabel({ uploaderID, uploadTime });
                return label ? { label } : null;
            })(),
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

function deleteRow(row: HTMLElement) {
    if (row.dataset.type === "folder") {
        openDeleteModal({
            type: "folder",
            id: row.dataset.id,
            name: row.dataset.name,
            parentId: row.dataset.parentId || state.currentFolderId,
        });
        return;
    }
    if (row.dataset.canDelete === "false") return;
    openDeleteModal({
        type: "file",
        id: Number(row.dataset.id),
        name: row.dataset.name,
        size: Number(row.dataset.size || 0),
        parentId: row.dataset.parentId || state.currentFolderId,
        source: row.dataset.source || "fs",
        canDelete: row.dataset.canDelete !== "false",
    });
}

function renameRow(row: HTMLElement) {
    if (row.dataset.canRename === "false") return;
    openRenameModal({
        type: row.dataset.type,
        id: row.dataset.type === "folder" ? row.dataset.id : Number(row.dataset.id),
        name: row.dataset.name,
        size: Number(row.dataset.size || 0),
        parentId: row.dataset.parentId || state.currentFolderId,
        source: row.dataset.source || "fs",
    });
}

function activateRow(row: HTMLElement) {
    if (isSearchMode()) {
        row.dispatchEvent(new MouseEvent("dblclick", { bubbles: true, cancelable: true }));
        return;
    }
    if (row.dataset.type === "folder") {
        navigateToFolder(row.dataset.id as string, row.dataset.name as string);
        return;
    }
    if (row.dataset.type !== "file") return;
    if (isVideoFile(row.dataset.name || "")) {
        window.initVideoPlayback(
            Number(row.dataset.id),
            row.dataset.name,
            Number(row.dataset.size || 0),
            row.dataset.encrypted === "true",
        );
        return;
    }
    if (canOpenFileViewer(row.dataset.name || "")) {
        void openFileViewer({
            id: Number(row.dataset.id),
            name: row.dataset.name || "File",
            size: Number(row.dataset.size || 0),
            encrypted: row.dataset.encrypted === "true",
        });
        return;
    }
    window.initDownload(Number(row.dataset.id), row.dataset.name, Number(row.dataset.size || 0));
}

function fillVisibleFolderStats(folders: any[], parentId: string, folderEpoch: number) {
    if (!folders.length) return;
    calculateVisibleFolderStats(parentId)
        .then((stats) => {
            if (state.folderSizeEpoch !== folderEpoch) return;
            if (state.currentFolderId !== parentId) return;
            applyFolderStats(folders, (id) => stats.get(id) ?? { bytes: 0, latestUpload: 0 });
        })
        .catch(() => {
            if (state.folderSizeEpoch !== folderEpoch) return;
            if (state.currentFolderId !== parentId) return;
            applyFolderStats(folders, () => null);
        });
}

// applyFolderStats stores the resolved subtree byte count and latest upload
// time on each folder row alongside their labels so the size and date columns
// can sort folders; null marks a failed lookup.
function applyFolderStats(folders: any[], statsForID: (id: string) => FolderStats | null) {
    const folderIDs = new Set(
        folders
            .map((folder) => String(folder?.id || ""))
            .filter(Boolean),
    );
    if (!folderIDs.size) return;

    updateFileListRows((rows) => {
        let changed = false;
        const nextRows = rows.map((row) => {
            if (row.kind !== "folder" || !folderIDs.has(row.id)) return row;
            const stats = statsForID(row.id);
            const size = stats?.bytes ?? 0;
            const modifiedTime = stats?.latestUpload ?? 0;
            const sizeLabel = stats === null ? "—" : formatBytes(size);
            const metaLabel = modifiedTime > 0 ? formatDate(modifiedTime) : "—";
            if (row.size === size && row.modifiedTime === modifiedTime
                && row.sizeLabel === sizeLabel && row.metaLabel === metaLabel) return row;
            changed = true;
            return { ...row, size, modifiedTime, sizeLabel, metaLabel };
        });
        return changed ? nextRows : rows;
    });
}

export function refreshFiles() {
    if (state.virtualView === "photos") {
        clearSelection();
        setPhotosMode(true);
        void renderGallery();
        return;
    }
    setPhotosMode(false);

    const list = document.getElementById("file-list") as HTMLElement;
    const storageUsed = document.getElementById("storage-used");
    const requestedFolderId = state.currentFolderId;
    resetFolderCaches();
    const folderEpoch = state.folderSizeEpoch;
    clearSelection();

    // Preserve scroll on a same-folder re-render (upload, delete, rename,
    // sync); navigation into a different folder still starts at the top.
    // Captured before the "Loading…" wipe resets scrollTop.
    const prevScrollTop = list.scrollTop;
    const keepScroll = lastRenderedFolderId === requestedFolderId;

    renderFileState(list, "loading", "Loading files");
    if (storageUsed) {
        storageUsed.innerText = "Calculating... / Unlimited";
        GetStorageUsed()
            .then((bytes) => {
                const value = Number(bytes);
                if (!Number.isFinite(value) || value < 0) {
                    storageUsed.innerText = "— / Unlimited";
                    return;
                }
                storageUsed.innerText = `${formatBytes(value)} / Unlimited`;
            })
            .catch(() => {
                storageUsed.innerText = "— / Unlimited";
            });
    }

    let folderErr: any = null;
    const folderPromise = apiGetFolderContents(requestedFolderId).catch((err) => {
        folderErr = err;
        console.error("GetFolderContents failed:", err);
        return null;
    });

    Promise.all([folderPromise]).then(async ([fs]) => {
        if (folderErr || !fs) {
            if (state.currentFolderId !== requestedFolderId) return;
            const msg = String(folderErr?.message || folderErr || "Failed to load files");
            renderFileState(list, "error", "Could not load this folder", msg, {
                label: "Retry",
                onClick: () => window.refreshFiles(),
            });
            return;
        }

        const folders = Array.isArray(fs?.folders) ? fs.folders : [];
        const fsFiles = Array.isArray(fs?.files) ? fs.files : [];

        const fsFileItems = fsFiles.map((f) => {
            const encrypted = !!f.encrypted;
            const plaintextSize = Number(f.plaintextSize || 0);
            // For encrypted files, the displayed size should be the
            // original plaintext size, not the on-wire ciphertext.
            const displaySize = encrypted && plaintextSize > 0 ? plaintextSize : f.size;
            return {
                source: "fs",
                id: f.msgId,
                name: f.name,
                size: displaySize,
                date: f.uploadTime,
                uploaderID: Number(f.uploaderId || 0),
                encrypted,
            };
        });

        const finalize = async (tgFiles: any[] = [], preserveCurrentScroll = false) => {
            if (state.folderSizeEpoch !== folderEpoch) return;
            if (state.currentFolderId !== requestedFolderId) return;
            const telegramFiles = Array.isArray(tgFiles) ? tgFiles : [];
            const scrollTopForRender = preserveCurrentScroll ? list.scrollTop : prevScrollTop;
            const keepScrollForRender = preserveCurrentScroll || keepScroll;
            let fsIDs;
            if (requestedFolderId === "" && telegramFiles.length > 0) {
                try {
                    fsIDs = new Set((await getAllFsMsgIDs()).filter((id) => typeof id === "number"));
                } catch (err) {
                    console.error("GetAllFsMsgIDs failed:", err);
                    fsIDs = new Set();
                }
            } else {
                fsIDs = new Set(fsFileItems.map((f) => f.id));
            }

            const tgFileItems = telegramFiles
                .filter((f) => !fsIDs.has(f.id))
                .map((f) => ({
                    source: "tg",
                    id: f.id,
                    name: f.name,
                    size: f.size,
                    date: f.date,
                }));

            const files = [...fsFileItems, ...tgFileItems];

            if (state.currentFolderId !== requestedFolderId) return;

            folders.sort((a, b) => (a.name || "").localeCompare(b.name || ""));
            files.sort((a, b) => (b.date || 0) - (a.date || 0));

            // Pending optimistic-create rows scoped to the current parent.
            // Computed up front so the empty-folder branch can include
            // them — otherwise creating a folder inside an empty one
            // would briefly show "This folder is empty" instead of the
            // ghost row.
            const pendingForParent = [];
            for (const [tempId, op] of state.pendingFolderOps.entries()) {
                if (op.parentId === requestedFolderId) {
                    pendingForParent.push({ tempId, name: op.name });
                }
            }

            if (folders.length === 0 && files.length === 0 && pendingForParent.length === 0) {
                renderFileState(list, "empty", "This folder is empty", "Upload files or create a folder to start organizing this drive.");
                lastRenderedFolderId = requestedFolderId;
                return;
            }

            const rows: FileListRow[] = [];

            // Pending CreateFolder ghost rows: rendered before real folders
            // so the just-clicked entry shows up at the top until the
            // backend confirms it.
            for (const op of pendingForParent) {
                rows.push(buildPendingFolderRow(op.tempId, op.name));
            }

            folders.forEach((folder) => {
                rows.push(buildFolderRow(folder, requestedFolderId));
            });

            files.forEach((file: any) => {
                const ownerOnly = canOwnerActOnFile(file);
                rows.push(buildFileRow(file, requestedFolderId, {
                    canDelete: ownerOnly,
                    canRename: ownerOnly,
                }));
            });

            renderFileListRows(list, rows, () => {
                if (state.folderSizeEpoch !== folderEpoch) return;
                if (state.currentFolderId !== requestedFolderId) return;

                syncDriveRowTabStops(list);
                fillVisibleFolderStats(folders, requestedFolderId, folderEpoch);

                // Restore prior scroll on a same-folder re-render. Before
                // pendingFocus so a just-uploaded/renamed file can still scroll
                // itself into view and win.
                lastRenderedFolderId = requestedFolderId;
                if (keepScrollForRender && state.currentFolderId === requestedFolderId) {
                    list.scrollTop = scrollTopForRender;
                }

                if (state.pendingFocus && state.pendingFocus.type === "file") {
                    const targetID = String(state.pendingFocus.id || "");
                    const targetRow = targetID ? list.querySelector(`.drive-row[data-type="file"][data-id="${CSS.escape(targetID)}"]`) : null;
                    state.pendingFocus = null;
                    if (targetRow) {
                        const interactive = Array.from(list.querySelectorAll(".drive-row"));
                        const idx = interactive.indexOf(targetRow);
                        clearSelection();
                        if (idx >= 0) selectRow(targetRow, idx);
                        setFocusedRow(targetRow as HTMLElement);
                        try {
                            targetRow.scrollIntoView({ block: "center" });
                        } catch {}
                    }
                }

                resolveUploaderChipsForRows(rows, () => (
                    state.folderSizeEpoch === folderEpoch
                    && state.currentFolderId === requestedFolderId
                ));
            });
        };

        await finalize();

        if (requestedFolderId === "" && state.currentFolderId === requestedFolderId) {
            GetFileList()
                .then(async (tgFiles) => {
                    if (state.folderSizeEpoch !== folderEpoch) return;
                    if (state.currentFolderId !== requestedFolderId) return;
                    const telegramFiles = Array.isArray(tgFiles) ? tgFiles : [];
                    state.telegramRootCache = telegramFiles;
                    await finalize(telegramFiles, true);
                })
                .catch((err) => {
                    console.error("GetFileList failed:", err);
                });
        }
    });
}

// Delegated row interactions. Listeners live on the #file-list container and
// read each row's data-* attributes, so re-rendering rows costs no listener
// churn and Svelte can freely replace keyed rows.
function isSearchMode() {
    return String(state.searchQuery || "").trim() !== "";
}

function handleListClick(e: MouseEvent) {
    // Search results still own their row handlers. Ignore those events here
    // so downloads/open/double-click navigation do not fire twice.
    if (isSearchMode()) return;

    const row = (e.target as HTMLElement).closest(".drive-row") as HTMLElement | null;
    if (!row) return;

    if (row.dataset.type === "folder") {
        if ((e.target as HTMLElement).closest("button.download-folder")) {
            enqueueFolderDownload(row.dataset.id, row.dataset.name);
            return;
        }
        setFocusedRow(row, { preventScroll: true });
        handleRowSelection(row, e);
        return;
    }
    if (row.dataset.type === "file") {
        if ((e.target as HTMLElement).closest("button.download")) {
            window.initDownload(Number(row.dataset.id), row.dataset.name, Number(row.dataset.size || 0));
            return;
        }
        if ((e.target as HTMLElement).closest("button.play-video")) {
            window.initVideoPlayback(
                Number(row.dataset.id),
                row.dataset.name,
                Number(row.dataset.size || 0),
                row.dataset.encrypted === "true",
            );
            return;
        }
        if ((e.target as HTMLElement).closest("button.open-file")) {
            void openFileViewer({
                id: Number(row.dataset.id),
                name: row.dataset.name || "File",
                size: Number(row.dataset.size || 0),
                encrypted: row.dataset.encrypted === "true",
            });
            return;
        }
        if ((e.target as HTMLElement).closest("button")) return;
        setFocusedRow(row, { preventScroll: true });
        handleRowSelection(row, e);
    }
}

function handleListKeyDown(e: KeyboardEvent) {
    const target = e.target as HTMLElement | null;
    if (target?.closest("button, input, textarea, select, [contenteditable='true']")) return;

    const list = document.getElementById("file-list") as HTMLElement | null;
    if (!list) return;
    const rows = interactiveRows(list);
    if (!rows.length) return;

    const current = activeRowFromEventTarget(e.target) || rows[0];
    const currentIndex = Math.max(0, rows.indexOf(current));
    let next: HTMLElement | null = null;

    switch (e.key) {
        case "ArrowDown":
            next = rows[Math.min(rows.length - 1, currentIndex + 1)];
            break;
        case "ArrowUp":
            next = rows[Math.max(0, currentIndex - 1)];
            break;
        case "Home":
            next = rows[0];
            break;
        case "End":
            next = rows[rows.length - 1];
            break;
        case " ":
            e.preventDefault();
            handleRowSelection(current, e);
            return;
        case "Enter":
            e.preventDefault();
            activateRow(current);
            return;
        case "F2":
            e.preventDefault();
            renameRow(current);
            return;
        case "Delete":
            e.preventDefault();
            deleteRow(current);
            return;
        case "ContextMenu":
            e.preventDefault();
            triggerRowContextMenu(current);
            return;
        case "F10":
            if (!e.shiftKey) return;
            e.preventDefault();
            triggerRowContextMenu(current);
            return;
        default:
            return;
    }

    if (!next) return;
    e.preventDefault();
    setFocusedRow(next, { preventScroll: false });
}

function handleListDblClick(e: MouseEvent) {
    if (isSearchMode()) return;

    const row = (e.target as HTMLElement).closest(".drive-row") as HTMLElement | null;
    if (!row) return;

    if (row.dataset.type === "folder") {
        navigateToFolder(row.dataset.id as string, row.dataset.name as string);
        return;
    }
    if (row.dataset.type === "file") {
        // Rename only from the name area and only when allowed.
        if (!(e.target as HTMLElement).closest(".row-name")) return;
        if (isVideoFile(row.dataset.name || "")) {
            e.preventDefault();
            const selection = window.getSelection?.();
            if (selection) selection.removeAllRanges();
            window.initVideoPlayback(
                Number(row.dataset.id),
                row.dataset.name,
                Number(row.dataset.size || 0),
                row.dataset.encrypted === "true",
            );
            return;
        }
        if (canOpenFileViewer(row.dataset.name || "")) {
            e.preventDefault();
            const selection = window.getSelection?.();
            if (selection) selection.removeAllRanges();
            void openFileViewer({
                id: Number(row.dataset.id),
                name: row.dataset.name || "File",
                size: Number(row.dataset.size || 0),
                encrypted: row.dataset.encrypted === "true",
            });
            return;
        }
        if (row.dataset.canRename !== "true") return;
        e.preventDefault();
        const selection = window.getSelection?.();
        if (selection) selection.removeAllRanges();
        openRenameModal({
            type: "file",
            id: Number(row.dataset.id),
            name: row.dataset.name,
            size: Number(row.dataset.size || 0),
            parentId: state.currentFolderId,
            source: row.dataset.source || "fs",
        });
    }
}

function handleListDragStart(e: DragEvent) {
    const handle = (e.target as HTMLElement | null)?.closest?.(".row-name[draggable='true']") as HTMLElement | null;
    const row = handle?.closest(".drive-row[data-type='folder'], .drive-row[data-type='file']") as HTMLElement | null;
    if (!row) return;

    const selection = window.getSelection?.();
    if (selection) selection.removeAllRanges();

    if (e.dataTransfer) {
        e.dataTransfer.effectAllowed = "move";
        try {
            e.dataTransfer.setData("text/plain", "tdrive-move");
        } catch {}
    }

    if (row.dataset.type === "folder") {
        startDrag(row, {
            type: "folder",
            id: String(row.dataset.id || ""),
            name: row.dataset.name || "Folder",
            parentId: row.dataset.parentId || state.currentFolderId,
            row,
        }, row.dataset.parentId || state.currentFolderId);
        return;
    }

    startDrag(row, {
        type: "file",
        id: Number(row.dataset.id || 0),
        name: row.dataset.name || "File",
        size: Number(row.dataset.size || 0),
        parentId: row.dataset.parentId || state.currentFolderId,
        source: row.dataset.source || "fs",
        row,
    }, row.dataset.parentId || state.currentFolderId);
}

function handleListDragOver(e: DragEvent) {
    if (!state.dragState) return;
    const row = (e.target as HTMLElement | null)?.closest?.(".drive-row[data-type='folder']") as HTMLElement | null;
    if (!row) return;
    const allowed = canDropOnFolder(row.dataset.id || "");
    setDropHighlight(row, allowed);
    if (e.dataTransfer) e.dataTransfer.dropEffect = allowed ? "move" : "none";
    if (allowed) e.preventDefault();
}

function handleListDragLeave(e: DragEvent) {
    const row = (e.target as HTMLElement | null)?.closest?.(".drive-row[data-type='folder']") as HTMLElement | null;
    if (!row) return;
    if (e.relatedTarget && row.contains(e.relatedTarget as Node)) return;
    if (state.dragOverEl === row) {
        row.classList.remove("drop-target");
        row.classList.remove("drop-denied");
        state.dragOverEl = null;
    }
}

async function handleListDrop(e: DragEvent) {
    if (!state.dragState) return;
    const row = (e.target as HTMLElement | null)?.closest?.(".drive-row[data-type='folder']") as HTMLElement | null;
    if (!row) return;
    const folderID = row.dataset.id || "";
    if (!canDropOnFolder(folderID)) return;
    e.preventDefault();
    e.stopPropagation();
    if (state.dragOverEl === row) state.dragOverEl = null;
    row.classList.remove("drop-target");
    row.classList.remove("drop-denied");
    await performDropMove(folderID);
}

export function setupFileListWindowBindings() {
    window.refreshFiles = refreshFiles;

    window.initDownload = function(id, name, size) {
        enqueueDownload(id, name, size);
    };

    window.initVideoPlayback = function(id, name, size, encrypted) {
        void openVideoModal({
            id: Number(id || 0),
            name: String(name || ""),
            size: Number(size || 0),
            encrypted: encrypted === true || encrypted === "true",
        });
    };

    const list = document.getElementById("file-list");
    if (list) {
        list.addEventListener("click", handleListClick);
        list.addEventListener("dblclick", handleListDblClick);
        list.addEventListener("keydown", handleListKeyDown);
        list.addEventListener("dragstart", handleListDragStart);
        list.addEventListener("dragend", endRowDrag);
        list.addEventListener("dragover", handleListDragOver);
        list.addEventListener("dragleave", handleListDragLeave);
        list.addEventListener("drop", (event) => {
            void handleListDrop(event);
        });
    }

    // Note: window.initDelete and window.initDeleteFolder are set up in main.js
    // to avoid circular dependency issues with the delete modal
}
