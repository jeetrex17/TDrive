// File list rendering for TDrive frontend

import { state, resetFolderCaches } from '../state.js';
import { icons } from '../constants.js';
import { escapeHtml, splitNameAndExt, formatDate, formatBytes } from '../utils.js';
import { clearSelection, handleRowSelection } from './selection.js';
import { openRenameModal } from './modals/rename.js';
import { navigateToFolder } from './navigation.js';
import { beginRowDrag, endRowDrag, canDropOnFolder, setDropHighlight, performDropMove } from './drag-drop.js';
import {
    GetFileList, DownloadFile, DeleteFile, GetStorageUsed
} from '../../wailsjs/go/main/App';
import { showDownloadProgress, hideDownloadProgress } from './transfers.js';

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

export async function calculateFolderTotalBytes(folderID, epoch) {
    const id = String(folderID || "");
    if (!id) return 0;

    const cached = state.folderSizeCache.get(id);
    if (typeof cached === "number") return cached;

    const inFlight = state.folderSizeInFlight.get(id);
    if (inFlight) return inFlight;

    const promise = (async () => {
        let total = 0;
        const visited = new Set();
        const queue = [id];

        while (queue.length) {
            const cur = queue.shift();
            if (!cur || visited.has(cur)) continue;
            visited.add(cur);

            let contents;
            try {
                contents = await getFolderContents(cur);
            } catch {
                contents = { folders: [], files: [] };
            }

            const files = Array.isArray(contents?.files) ? contents.files : [];
            for (const file of files) {
                const size = Number(file?.size ?? 0);
                if (Number.isFinite(size) && size > 0) total += size;
            }

            const folders = Array.isArray(contents?.folders) ? contents.folders : [];
            for (const folder of folders) {
                const next = String(folder?.id || "");
                if (next) queue.push(next);
            }
        }

        return total;
    })();

    state.folderSizeInFlight.set(id, promise);

    try {
        const bytes = await promise;
        if (state.folderSizeEpoch === epoch) state.folderSizeCache.set(id, bytes);
        return bytes;
    } finally {
        if (state.folderSizeEpoch === epoch) state.folderSizeInFlight.delete(id);
    }
}

async function collectAllFsMsgIDs(rootFS) {
    const msgIDs = new Set();
    const visitedFolders = new Set();
    const queue = [];

    const rootFiles = Array.isArray(rootFS?.files) ? rootFS.files : [];
    rootFiles.forEach((file) => {
        if (typeof file?.msg_id === "number") msgIDs.add(file.msg_id);
    });

    const rootFolders = Array.isArray(rootFS?.folders) ? rootFS.folders : [];
    rootFolders.forEach((folder) => {
        if (folder?.id) queue.push(folder.id);
    });

    while (queue.length) {
        const folderID = queue.shift();
        if (!folderID || visitedFolders.has(folderID)) continue;
        visitedFolders.add(folderID);

        try {
            const contents = await getFolderContents(folderID);
            const files = Array.isArray(contents?.files) ? contents.files : [];
            const folders = Array.isArray(contents?.folders) ? contents.folders : [];

            files.forEach((file) => {
                if (typeof file?.msg_id === "number") msgIDs.add(file.msg_id);
            });
            folders.forEach((folder) => {
                if (folder?.id) queue.push(folder.id);
            });
        } catch (err) {
            console.error("collectAllFsMsgIDs: GetFolderContents failed for", folderID, err);
        }
    }

    return msgIDs;
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

    const folderPromise = getFolderContents(requestedFolderId).catch((err) => {
        console.error("GetFolderContents failed:", err);
        return { folders: [], files: [] };
    });

    const tgPromise = requestedFolderId === ""
        ? GetFileList().catch((err) => {
            console.error("GetFileList failed:", err);
            return [];
        })
        : Promise.resolve([]);

    Promise.all([folderPromise, tgPromise]).then(([fs, tgFiles]) => {
        const folders = Array.isArray(fs?.folders) ? fs.folders : [];
        const fsFiles = Array.isArray(fs?.files) ? fs.files : [];
        const telegramFiles = Array.isArray(tgFiles) ? tgFiles : [];

        const fsFileItems = fsFiles.map((f) => ({
            source: "fs",
            id: f.msg_id,
            name: f.name,
            size: f.size,
            date: f.upload_time,
        }));

        const finalize = async () => {
            const fsIDs = requestedFolderId === "" ? await collectAllFsMsgIDs(fs) : new Set(fsFileItems.map((f) => f.id));

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

            if (folders.length === 0 && files.length === 0) {
                list.innerHTML = '<div style="padding:20px; color:#565f89;">This folder is empty.</div>';
                return;
            }

            list.innerHTML = "";

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
                    calculateFolderTotalBytes(folder.id, folderEpoch)
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
                row.dataset.canDelete = "true";

                row.innerHTML = `
                    <div class="row-name">
                        <span class="file-ext-text" aria-hidden="true">${escapeHtml(ext)}</span>
                        ${escapeHtml(base)}
                    </div>
                    <div class="row-meta">${formatDate(file.date)}</div>
                    <div class="row-meta">${formatBytes(file.size)}</div>
                    <div class="row-actions">
                        <button class="action-icon download" type="button" title="Download">${icons.download}</button>
                    </div>
                `;

                const downloadBtn = row.querySelector("button.download");
                if (downloadBtn) {
                    downloadBtn.addEventListener("click", () => window.initDownload(file.id, file.name));
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
        };

        finalize();
    });
}

export function setupFileListWindowBindings() {
    window.refreshFiles = refreshFiles;

    window.initDownload = function(id, name) {
        const status = document.getElementById("status-msg");
        state.activeTransfer = "download";
        if (status) status.innerText = "Downloading…";

        showDownloadProgress(0, name);

        let succeeded = false;

        DownloadFile(id)
            .then((res) => {
                succeeded = true;
                alert(res);
            })
            .catch((err) => {
                console.error("Download failed:", err);
                alert("Download failed. Check console/logs.");
            })
            .finally(() => {
                if (state.activeTransfer === "download") state.activeTransfer = null;
                hideDownloadProgress(succeeded ? "done" : "failed");
                if (status) status.innerText = "Ready";
            });
    };

    // Note: window.initDelete and window.initDeleteFolder are set up in main.js
    // to avoid circular dependency issues with the delete modal
}
