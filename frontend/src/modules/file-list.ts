// File list rendering for TDrive frontend

import { state, resetFolderCaches } from '../state';
import { icons } from '../constants';
import { escapeHtml, splitNameAndExt, formatDate, formatBytes } from '../utils';
import { clearSelection, handleRowSelection, selectRow, getRowKey } from './selection';
import { openRenameModal } from './modals/rename';
import { navigateToFolder } from './navigation';
import { beginRowDrag, endRowDrag, canDropOnFolder, setDropHighlight, performDropMove } from './drag-drop';
import {
    GetFileList, GetStorageUsed,
} from '../../wailsjs/go/main/App';
import { getFolderContents as apiGetFolderContents } from '../api';
import { calculateFolderTotalBytes, getAllFsMsgIDs } from './drive-data';
import { refreshFolderIndex, collectDescendants } from './folder-index';
import { enqueueDownload } from './transfers';
import { populateUploaderChips, uploaderChipHTML } from './uploaders';
import { renderGallery, setPhotosMode } from './gallery';

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

// fillUploaderSlot writes the uploader chip text into the [data-uploader-slot]
// span of the given row. No-op if the chip wouldn't apply (personal drive,
// uploader unknown, etc.) — uploaderChipHTML returns null in those cases
// and the slot stays empty.
//
// Safe to call before the user-name cache is populated; the slot stays
// empty and a follow-up populateUploaderChips pass fills it once names
// resolve.
export function fillUploaderSlot(row: HTMLElement | null, file: any) {
    if (!row) return;
    const slot = row.querySelector('[data-uploader-slot]');
    if (!slot) return;
    const html = uploaderChipHTML(file);
    slot.innerHTML = html ?? '';
}

// Tracks the folder whose rows are currently rendered, so a same-folder
// re-render can restore the scroll position instead of jumping to the top.
let lastRenderedFolderId: string | null = null;

export function resetFileListScrollRestore() {
    lastRenderedFolderId = null;
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

    list.innerHTML = '<div style="padding:20px; color:#565f89;">Loading...</div>';
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

    const tgPromise = requestedFolderId === ""
        ? GetFileList().catch((err) => {
            console.error("GetFileList failed:", err);
            return [];
        })
        : Promise.resolve([]);

    Promise.all([folderPromise, tgPromise]).then(([fs, tgFiles]) => {
        if (folderErr || !fs) {
            if (state.currentFolderId !== requestedFolderId) return;
            const msg = String(folderErr?.message || folderErr || "Failed to load files");
            list.innerHTML = `
                <div style="padding:20px; color:#c0caf5;">
                    <div style="font-weight:700; margin-bottom:8px;">Could not load this folder</div>
                    <div style="color:#8b95c5; margin-bottom:12px;">${escapeHtml(msg)}</div>
                    <button class="secondary-btn" type="button" onclick="triggerRefresh()">Retry</button>
                </div>
            `;
            return;
        }

        const folders = Array.isArray(fs?.folders) ? fs.folders : [];
        const fsFiles = Array.isArray(fs?.files) ? fs.files : [];
        const telegramFiles = Array.isArray(tgFiles) ? tgFiles : [];
        if (requestedFolderId === "") state.telegramRootCache = telegramFiles;

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

        const finalize = async () => {
            let fsIDs;
            if (requestedFolderId === "") {
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
                list.innerHTML = '<div style="padding:20px; color:#565f89;">This folder is empty.</div>';
                lastRenderedFolderId = requestedFolderId;
                return;
            }

            list.innerHTML = "";

            // Pending CreateFolder ghost rows: rendered before real folders
            // so the just-clicked entry shows up at the top until the
            // backend confirms it.
            for (const op of pendingForParent) {
                list.appendChild(buildPendingFolderRow(op.tempId, op.name));
            }

            folders.forEach((folder) => {
                const row = document.createElement("div");
                row.className = "file-row drive-row folder-row";
                row.dataset.type = "folder";
                row.dataset.id = folder.id;
                row.dataset.name = folder.name;
                row.dataset.parentId = requestedFolderId;

                row.innerHTML = `
                    <div class="row-name">
                        <span class="folder-chip" aria-hidden="true">${icons.folder}</span>
                        ${escapeHtml(folder.name)}
                    </div>
                    <div class="row-meta">—</div>
                    <div class="row-meta folder-size">…</div>
                    <div class="row-actions">
                        <button class="action-icon open-folder" type="button" title="Open">${icons.open}</button>
                    </div>
                `;

                const folderNameEl = row.querySelector(".row-name") as HTMLElement | null;
                if (folderNameEl) {
                    folderNameEl.draggable = true;
                    folderNameEl.addEventListener("dragstart", (e) => {
                        const selection = window.getSelection?.();
                        if (selection) selection.removeAllRanges();

                        if (e.dataTransfer) {
                            e.dataTransfer.effectAllowed = "move";
                            try {
                                e.dataTransfer.setData("text/plain", "tdrive-move");
                            } catch {}
                        }

                        const folderID = String(folder.id || row.dataset.id || "");
                        startDrag(row, {
                            type: "folder",
                            id: folderID,
                            name: folder.name || row.dataset.name || "Folder",
                            parentId: requestedFolderId,
                            row,
                        }, requestedFolderId);
                    });
                    folderNameEl.addEventListener("dragend", endRowDrag);
                }

                row.addEventListener("dragover", (e) => {
                    if (!state.dragState) return;
                    const allowed = canDropOnFolder(folder.id);
                    setDropHighlight(row, allowed);
                    if (e.dataTransfer) e.dataTransfer.dropEffect = allowed ? "move" : "none";
                    if (allowed) e.preventDefault();
                });
                row.addEventListener("dragleave", (e) => {
                    if (e.relatedTarget && row.contains(e.relatedTarget as Node)) return;
                    if (state.dragOverEl === row) {
                        row.classList.remove("drop-target");
                        row.classList.remove("drop-denied");
                        state.dragOverEl = null;
                    }
                });
                row.addEventListener("drop", async (e) => {
                    if (!state.dragState) return;
                    const allowed = canDropOnFolder(folder.id);
                    if (!allowed) return;
                    e.preventDefault();
                    e.stopPropagation();
                    if (state.dragOverEl === row) state.dragOverEl = null;
                    row.classList.remove("drop-target");
                    row.classList.remove("drop-denied");
                    await performDropMove(folder.id);
                });

                list.appendChild(row);

                const sizeEl = row.querySelector(".folder-size");
                if (sizeEl) {
                    calculateFolderTotalBytes(folder.id)
                        .then((bytes) => {
                            if (state.folderSizeEpoch !== folderEpoch) return;
                            if (state.currentFolderId !== requestedFolderId) return;
                            if (!row.isConnected) return;
                            sizeEl.textContent = formatBytes(bytes);
                        })
                        .catch(() => {
                            if (state.folderSizeEpoch !== folderEpoch) return;
                            if (state.currentFolderId !== requestedFolderId) return;
                            if (!row.isConnected) return;
                            sizeEl.textContent = "—";
                        });
                }
            });

            files.forEach((file: any) => {
                const { base, ext } = splitNameAndExt(file.name);
                const row = document.createElement("div");
                row.className = "file-row drive-row";
                row.dataset.type = "file";
                row.dataset.id = String(file.id);
                row.dataset.name = String(file.name || "");
                row.dataset.source = String(file.source || "fs");
                row.dataset.size = String(file.size || 0);
                row.dataset.parentId = requestedFolderId;
                row.dataset.uploaderId = String(file.uploaderID || 0);
                row.dataset.uploadTime = String(file.date || 0);
                const ownerOnly = canOwnerActOnFile(file);
                row.dataset.canDelete = ownerOnly ? "true" : "false";
                row.dataset.canRename = ownerOnly ? "true" : "false";

                const lockBadge = file.encrypted
                    ? `<span class="file-lock-badge" title="Encrypted" aria-label="Encrypted"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path stroke-linecap="round" stroke-linejoin="round" d="M5 11h14a1 1 0 011 1v8a1 1 0 01-1 1H5a1 1 0 01-1-1v-8a1 1 0 011-1z"/><path stroke-linecap="round" stroke-linejoin="round" d="M8 11V7a4 4 0 118 0v4"/></svg></span>`
                    : '';
                row.innerHTML = `
                    <div class="row-name">
                        <span class="file-ext-text" aria-hidden="true">${escapeHtml(ext)}</span>
                        ${lockBadge}${escapeHtml(base)}
                        <span class="uploader-chip" data-uploader-slot></span>
                    </div>
                    <div class="row-meta">${formatDate(file.date)}</div>
                    <div class="row-meta">${formatBytes(file.size)}</div>
                    <div class="row-actions">
                        <button class="action-icon download" type="button" title="Download">${icons.download}</button>
                    </div>
                `;
                fillUploaderSlot(row, {
                    uploaderID: file.uploaderID,
                    uploadTime: file.date,
                });

                const nameEl = row.querySelector(".row-name") as HTMLElement | null;
                if (nameEl) {
                    nameEl.draggable = true;
                    nameEl.addEventListener("dragstart", (e) => {
                        const selection = window.getSelection?.();
                        if (selection) selection.removeAllRanges();

                        if (e.dataTransfer) {
                            e.dataTransfer.effectAllowed = "move";
                            try {
                                e.dataTransfer.setData("text/plain", "tdrive-move");
                            } catch {}
                        }

                        startDrag(row, {
                            type: "file",
                            id: Number(file.id),
                            name: file.name || row.dataset.name || "File",
                            size: Number(file.size || row.dataset.size || 0),
                            parentId: requestedFolderId,
                            source: file.source || row.dataset.source || "fs",
                            row,
                        }, requestedFolderId);
                    });
                    nameEl.addEventListener("dragend", endRowDrag);
                }
                list.appendChild(row);
            });

            // Restore prior scroll on a same-folder re-render. Before
            // pendingFocus so a just-uploaded/renamed file can still scroll
            // itself into view and win.
            lastRenderedFolderId = requestedFolderId;
            if (keepScroll && state.currentFolderId === requestedFolderId) {
                list.scrollTop = prevScrollTop;
            }

            if (state.pendingFocus && state.pendingFocus.type === "file") {
                const targetID = String(state.pendingFocus.id || "");
                const targetRow = targetID ? list.querySelector(`.drive-row[data-type="file"][data-id="${targetID}"]`) : null;
                state.pendingFocus = null;
                if (targetRow) {
                    const rows = Array.from(list.querySelectorAll(".drive-row"));
                    const idx = rows.indexOf(targetRow);
                    clearSelection();
                    if (idx >= 0) selectRow(targetRow, idx);
                    try {
                        targetRow.scrollIntoView({ block: "center" });
                    } catch {}
                }
            }

            // Resolve any missing uploader names and inject chips. Fire-
            // and-forget — rows are already shown without chips and will
            // get them populated within ~one round-trip.
            populateUploaderChips(list);
        };

        finalize();
    });
}

// buildPendingFolderRow renders an in-flight CreateFolder as a ghost row.
// It looks like a real folder row but is dimmed and has no actions; click
// is a no-op. Removed from the list once the Wails call resolves and
// refreshFiles re-renders.
function buildPendingFolderRow(tempId: string, name: string) {
    const row = document.createElement("div");
    row.className = "file-row drive-row folder-row pending-folder";
    row.dataset.type = "pending-folder";
    row.dataset.tempId = tempId;
    row.title = "Creating…";
    row.innerHTML = `
        <div class="row-name">
            <span class="folder-chip" aria-hidden="true">${icons.folder}</span>
            ${escapeHtml(name)}
            <span class="pending-indicator" aria-hidden="true">·</span>
        </div>
        <div class="row-meta">Creating…</div>
        <div class="row-meta">—</div>
        <div class="row-actions"></div>
    `;
    return row;
}

// Delegated row interactions. Listeners live on the #file-list container and
// read each row's data-* attributes, so re-rendering rows costs no click
// listener churn. Drag handlers are still attached per row for now.
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
        if ((e.target as HTMLElement).closest("button.open-folder")) {
            navigateToFolder(row.dataset.id as string, row.dataset.name as string);
            return;
        }
        handleRowSelection(row, e);
        return;
    }
    if (row.dataset.type === "file") {
        if ((e.target as HTMLElement).closest("button.download")) {
            window.initDownload(Number(row.dataset.id), row.dataset.name, Number(row.dataset.size || 0));
            return;
        }
        if ((e.target as HTMLElement).closest("button")) return;
        handleRowSelection(row, e);
    }
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

export function setupFileListWindowBindings() {
    window.refreshFiles = refreshFiles;

    window.initDownload = function(id, name, size) {
        enqueueDownload(id, name, size);
    };

    const list = document.getElementById("file-list");
    if (list) {
        list.addEventListener("click", handleListClick);
        list.addEventListener("dblclick", handleListDblClick);
    }

    // Note: window.initDelete and window.initDeleteFolder are set up in main.js
    // to avoid circular dependency issues with the delete modal
}
