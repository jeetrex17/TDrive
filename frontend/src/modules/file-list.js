// File list rendering for TDrive frontend

import { state, resetFolderCaches } from '../state.js';
import { icons } from '../constants.js';
import { escapeHtml, splitNameAndExt, formatDate, formatBytes } from '../utils.js';
import { clearSelection, handleRowSelection, selectRow } from './selection.js';
import { openRenameModal } from './modals/rename.js';
import { navigateToFolder } from './navigation.js';
import { beginRowDrag, endRowDrag, canDropOnFolder, setDropHighlight, performDropMove } from './drag-drop.js';
import {
    GetFileList, DeleteFile, GetStorageUsed, GetOrphanedFiles,
} from '../../wailsjs/go/main/App';
import { enqueueDownload } from './transfers.js';
import { populateUploaderChips, uploaderChipHTML } from './uploaders.js';

export async function getFolderContents(parentID) {
    if (window.go?.main?.App?.GetFolderContents) {
        return window.go.main.App.GetFolderContents(parentID);
    }
    throw new Error("GetFolderContents is not available. Restart `wails dev` to regenerate bindings.");
}

export async function deleteFolder(folderID) {
    if (window.go?.main?.App?.DeleteFolder) {
        return window.go.main.App.DeleteFolder(folderID);
    }
    throw new Error("DeleteFolder is not available. Restart `wails dev` to regenerate bindings.");
}

export async function createFolder(name, parentID) {
    if (window.go?.main?.App?.CreateFolder) {
        return window.go.main.App.CreateFolder(name, parentID);
    }
    throw new Error("CreateFolder is not available. Restart `wails dev` to regenerate bindings.");
}

export async function calculateFolderTotalBytes(folderID) {
    const id = String(folderID || "");
    if (!id) return 0;

    if (window.go?.main?.App?.GetFolderSize) {
        const value = await window.go.main.App.GetFolderSize(id);
        const bytes = Number(value);
        return Number.isFinite(bytes) && bytes >= 0 ? bytes : 0;
    }
    throw new Error("GetFolderSize is not available. Restart `wails dev` to regenerate bindings.");
}

// canOwnerActOnFile returns true when the current user is allowed to
// rename/delete the given file. In personal drives it's always true (you
// uploaded everything). In shared drives it's only true when the file's
// recorded uploader matches the current user. Default-deny when uploader
// or self id is unknown.
export function canOwnerActOnFile(file) {
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
export function fillUploaderSlot(row, file) {
    if (!row) return;
    const slot = row.querySelector('[data-uploader-slot]');
    if (!slot) return;
    const html = uploaderChipHTML(file);
    slot.innerHTML = html ?? '';
}

async function getAllFsMsgIDs() {
    if (window.go?.main?.App?.GetAllFsMsgIDs) {
        const ids = await window.go.main.App.GetAllFsMsgIDs();
        return Array.isArray(ids) ? ids : [];
    }
    throw new Error("GetAllFsMsgIDs is not available. Restart `wails dev` to regenerate bindings.");
}

export async function buildFolderIndex() {
    const folders = [];
    const byId = new Map();
    const children = new Map();

    const addFolder = (folder) => {
        if (!folder?.id || byId.has(folder.id)) return;
        byId.set(folder.id, folder);
        folders.push(folder);
        const pid = folder.parent_id || "";
        if (!children.has(pid)) children.set(pid, []);
        children.get(pid).push(folder.id);
    };

    const queue = [""];
    const visited = new Set();

    while (queue.length) {
        const parentID = queue.shift();
        if (visited.has(parentID)) continue;
        visited.add(parentID);

        let contents;
        try {
            contents = await getFolderContents(parentID);
        } catch {
            contents = { folders: [] };
        }

        const sub = Array.isArray(contents?.folders) ? contents.folders : [];
        sub.forEach((folder) => {
            addFolder(folder);
            if (folder?.id) queue.push(folder.id);
        });
    }

    folders.forEach((folder) => {
        const pid = folder.parent_id || "";
        if (!children.has(pid)) children.set(pid, []);
        children.get(pid).sort((a, b) => (byId.get(a)?.name || "").localeCompare(byId.get(b)?.name || ""));
    });

    return { folders, byId, children };
}

export async function refreshFolderIndex() {
    if (state.folderIndexBuildPromise) return state.folderIndexBuildPromise;
    state.folderIndexBuildPromise = buildFolderIndex()
        .then((idx) => {
            state.folderIndexCache = idx;
            return idx;
        })
        .finally(() => {
            state.folderIndexBuildPromise = null;
        });
    return state.folderIndexBuildPromise;
}

export function collectDescendants(folderId, children) {
    const out = new Set();
    const stack = [folderId];
    while (stack.length) {
        const id = stack.pop();
        const kids = children.get(id) || [];
        for (const k of kids) {
            if (out.has(k)) continue;
            out.add(k);
            stack.push(k);
        }
    }
    return out;
}

export function refreshFiles() {
    if (state.virtualView === "orphaned") {
        return refreshOrphanView();
    }

    const list = document.getElementById("file-list");
    const storageUsed = document.getElementById("storage-used");
    const requestedFolderId = state.currentFolderId;
    resetFolderCaches();
    const folderEpoch = state.folderSizeEpoch;
    clearSelection();

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

    let folderErr = null;
    const folderPromise = getFolderContents(requestedFolderId).catch((err) => {
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
            const plaintextSize = Number(f.plaintext_size || 0);
            // For encrypted files, the displayed size should be the
            // original plaintext size, not the on-wire ciphertext.
            const displaySize = encrypted && plaintextSize > 0 ? plaintextSize : f.size;
            return {
                source: "fs",
                id: f.msg_id,
                name: f.name,
                size: displaySize,
                date: f.upload_time,
                uploaderID: Number(f.uploader_id || 0),
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

            // At root, surface a synthetic "Orphaned" entry if any files
            // exist whose parent points at a tombstoned/missing folder.
            // Real folders + files render below it. The query is cheap;
            // running it always at root keeps the bucket honest after
            // any folder delete.
            let orphanCount = 0;
            if (requestedFolderId === "") {
                try {
                    const orphans = await GetOrphanedFiles();
                    orphanCount = Array.isArray(orphans) ? orphans.length : 0;
                } catch (err) {
                    console.warn("GetOrphanedFiles failed:", err);
                }
            }

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

            if (folders.length === 0 && files.length === 0 && orphanCount === 0 && pendingForParent.length === 0) {
                list.innerHTML = '<div style="padding:20px; color:#565f89;">This folder is empty.</div>';
                return;
            }

            list.innerHTML = "";

            if (orphanCount > 0) {
                list.appendChild(buildOrphanEntryRow(orphanCount));
            }

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

                row.addEventListener("dblclick", () => navigateToFolder(folder.id, folder.name));
                row.addEventListener("click", (e) => {
                    if (e.target.closest("button.open-folder")) return navigateToFolder(folder.id, folder.name);
                    handleRowSelection(row, e);
                });

                const folderNameEl = row.querySelector(".row-name");
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
                        beginRowDrag(row, {
                            type: "folder",
                            id: folderID,
                            name: folder.name || row.dataset.name || "Folder",
                            parentId: requestedFolderId,
                            blocked: folderID ? new Set([folderID]) : new Set(),
                        });

                        if (folderID) {
                            refreshFolderIndex()
                                .then((index) => {
                                    if (!state.dragState || state.dragState.type !== "folder") return;
                                    if (String(state.dragState.id || "") !== folderID) return;
                                    const blocked = collectDescendants(folderID, index.children);
                                    blocked.add(folderID);
                                    state.dragState.blocked = blocked;
                                })
                                .catch(() => {});
                        }
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
                    if (e.relatedTarget && row.contains(e.relatedTarget)) return;
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

            files.forEach((file) => {
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

                const downloadBtn = row.querySelector("button.download");
                if (downloadBtn) {
                    downloadBtn.addEventListener("click", () => window.initDownload(file.id, file.name, file.size));
                }

                row.addEventListener("click", (e) => {
                    if (e.target.closest("button")) return;
                    handleRowSelection(row, e);
                });

                const nameEl = row.querySelector(".row-name");
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

                        beginRowDrag(row, {
                            type: "file",
                            id: Number(file.id),
                            name: file.name || row.dataset.name || "File",
                            size: Number(file.size || row.dataset.size || 0),
                            parentId: requestedFolderId,
                            source: file.source || row.dataset.source || "fs",
                        });
                    });
                    nameEl.addEventListener("dragend", endRowDrag);

                    nameEl.addEventListener("dblclick", (e) => {
                        e.preventDefault();
                        e.stopPropagation();
                        const selection = window.getSelection?.();
                        if (selection) selection.removeAllRanges();
                        if (!canOwnerActOnFile(file)) return;
                        openRenameModal({
                            type: "file",
                            id: file.id,
                            name: file.name,
                            size: Number(file.size || row.dataset.size || 0),
                            parentId: state.currentFolderId,
                            source: file.source || row.dataset.source || "fs",
                        });
                    });
                }
                list.appendChild(row);
            });

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
function buildPendingFolderRow(tempId, name) {
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

// buildOrphanEntryRow renders the synthetic "Orphaned (N)" entry that
// appears at the top of root listings. Click drops the user into the
// virtual orphan view (no real folder navigation; state.currentFolderId
// stays "").
function buildOrphanEntryRow(count) {
    const row = document.createElement("div");
    row.className = "file-row drive-row folder-row orphan-entry";
    row.dataset.type = "orphan-entry";
    row.innerHTML = `
        <div class="row-name">
            <span class="folder-chip" aria-hidden="true">${icons.folder}</span>
            Orphaned <span style="opacity:.6;">(${count})</span>
        </div>
        <div class="row-meta">—</div>
        <div class="row-meta">—</div>
        <div class="row-actions"></div>
    `;
    row.title = "Files whose parent folder was deleted. Click to view.";
    row.addEventListener("click", () => enterOrphanView());
    row.addEventListener("dblclick", () => enterOrphanView());
    return row;
}

export function enterOrphanView() {
    state.virtualView = "orphaned";
    refreshFiles();
}

export function exitOrphanView() {
    state.virtualView = null;
    refreshFiles();
}

async function refreshOrphanView() {
    const list = document.getElementById("file-list");
    const storageUsed = document.getElementById("storage-used");
    clearSelection();
    list.innerHTML = '<div style="padding:20px; color:#565f89;">Loading...</div>';
    if (storageUsed) {
        // Storage usage stays consistent across virtual views.
        GetStorageUsed()
            .then((bytes) => {
                const value = Number(bytes);
                storageUsed.innerText = (Number.isFinite(value) && value >= 0)
                    ? `${formatBytes(value)} / Unlimited`
                    : "— / Unlimited";
            })
            .catch(() => { storageUsed.innerText = "— / Unlimited"; });
    }

    let orphans;
    try {
        orphans = await GetOrphanedFiles();
    } catch (err) {
        console.error("GetOrphanedFiles failed:", err);
        list.innerHTML = '<div style="padding:20px; color:#c0caf5;">Failed to load orphan files.</div>';
        return;
    }
    orphans = Array.isArray(orphans) ? orphans : [];

    list.innerHTML = "";

    const banner = document.createElement("div");
    banner.className = "orphan-banner";
    banner.innerHTML = `
        <div>
            <strong>Orphaned files</strong> — these files lived in folders that were deleted.
            They aren't in any folder anymore, but they aren't deleted either.
        </div>
        <button id="orphan-back" class="secondary-btn" type="button">Back to root</button>
    `;
    list.appendChild(banner);
    banner.querySelector("#orphan-back")?.addEventListener("click", () => exitOrphanView());

    if (orphans.length === 0) {
        const empty = document.createElement("div");
        empty.style.cssText = "padding:20px; color:#565f89;";
        empty.textContent = "Nothing here. (If you've just refreshed and expected files, try Refresh again.)";
        list.appendChild(empty);
        return;
    }

    orphans.sort((a, b) => (Number(b.upload_time || 0)) - (Number(a.upload_time || 0)));

    orphans.forEach((file) => {
        const { base, ext } = splitNameAndExt(file.name);
        const row = document.createElement("div");
        row.className = "file-row drive-row";
        row.dataset.type = "file";
        row.dataset.id = String(file.msg_id);
        row.dataset.name = String(file.name || "");
        row.dataset.source = "fs";
        row.dataset.size = String(file.size || 0);
        row.dataset.parentId = "";
        row.dataset.uploaderId = String(file.uploader_id || 0);
        const fileShape = {
            id: file.msg_id,
            name: file.name,
            size: file.size,
            uploaderID: Number(file.uploader_id || 0),
        };
        const ownerOnly = canOwnerActOnFile(fileShape);
        row.dataset.canDelete = ownerOnly ? "true" : "false";
        row.dataset.canRename = ownerOnly ? "true" : "false";
        row.dataset.uploadTime = String(file.upload_time || 0);

        row.innerHTML = `
            <div class="row-name">
                <span class="file-ext-text" aria-hidden="true">${escapeHtml(ext)}</span>
                ${escapeHtml(base)}
                <span class="uploader-chip" data-uploader-slot></span>
            </div>
            <div class="row-meta">${formatDate(file.upload_time)}</div>
            <div class="row-meta">${formatBytes(file.size)}</div>
            <div class="row-actions">
                <button class="action-icon download" type="button" title="Download">${icons.download}</button>
            </div>
        `;
        row.querySelector("button.download")
            ?.addEventListener("click", () => window.initDownload(file.msg_id, file.name, file.size));
        row.addEventListener("click", (e) => {
            if (e.target.closest("button")) return;
            handleRowSelection(row, e);
        });
        list.appendChild(row);
    });

    populateUploaderChips(list);
}

export function setupFileListWindowBindings() {
    window.refreshFiles = refreshFiles;

    window.initDownload = function(id, name, size) {
        enqueueDownload(id, name, size);
    };

    // Note: window.initDelete and window.initDeleteFolder are set up in main.js
    // to avoid circular dependency issues with the delete modal
}
