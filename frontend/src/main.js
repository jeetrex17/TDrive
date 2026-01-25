
import { 
    CheckSystemStatus, SaveSetup,
    LoginPhoneNumber, SumbitCode, SumbitPassword, 
    GetFileList, DownloadFile, DeleteFile, 
    CheckLoginStatus, InitDrive, SelectFiles,
    RenameFile, RenameFolder, MoveFile, MoveFolder, MsgToTdriveSystem
} from '../wailsjs/go/main/App';

let pendingDeleteTarget = null; // { type: "file" | "folder", id: number|string, name?: string }
let pendingRenameTarget = null;
let pendingMoveTarget = null;
let currentFolderId = "";
let folderPath = []; // [{ id, name }]
let activeTransfer = null; // "download" | "upload" | null
let downloadProgressEl = null;
let downloadProgressFillEl = null;
let downloadProgressHideTimeout = null;
let lastLoginPhoneNumber = "";
let transferPillEl = null;
let transferSheetEl = null;
let transferUploadListEl = null;
let transferClearEl = null;
let uploadTransfers = new Map(); // id -> { id, name, size, parentId, progress, state }
let uploadBatch = null; // { total, done, failed }

const icons = {
    download: `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-4l-4 4m0 0l-4-4m4 4V4"/></svg>`,
    trash: `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16"/></svg>`,
    folder: `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path stroke-linecap="round" stroke-linejoin="round" d="M3 7a2 2 0 012-2h5l2 2h7a2 2 0 012 2v8a2 2 0 01-2 2H5a2 2 0 01-2-2V7z"/></svg>`,
    open: `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path stroke-linecap="round" stroke-linejoin="round" d="M9 5H7a2 2 0 00-2 2v10a2 2 0 002 2h10a2 2 0 002-2v-2"/><path stroke-linecap="round" stroke-linejoin="round" d="M14 3h7v7"/><path stroke-linecap="round" stroke-linejoin="round" d="M10 14L21 3"/></svg>`,
    edit: `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path stroke-linecap="round" stroke-linejoin="round" d="M12 20h9"/><path stroke-linecap="round" stroke-linejoin="round" d="M16.5 3.5a2.121 2.121 0 013 3L7 19l-4 1 1-4 12.5-12.5z"/></svg>`,
    move: `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path stroke-linecap="round" stroke-linejoin="round" d="M7 7h10v10H7z"/><path stroke-linecap="round" stroke-linejoin="round" d="M9 3h12v12"/><path stroke-linecap="round" stroke-linejoin="round" d="M3 9v12h12"/></svg>`,
};

function escapeHtml(input) {
    return String(input ?? "")
        .replace(/&/g, "&amp;")
        .replace(/</g, "&lt;")
        .replace(/>/g, "&gt;")
        .replace(/"/g, "&quot;")
        .replace(/'/g, "&#039;");
}

function splitNameAndExt(filename) {
    const name = typeof filename === "string" ? filename : "";
    const lastDot = name.lastIndexOf(".");
    if (lastDot <= 0 || lastDot === name.length - 1) {
        return { base: name, ext: "FILE" };
    }
    const base = name.slice(0, lastDot);
    const rawExt = name.slice(lastDot + 1);
    const ext = rawExt.replace(/[^a-z0-9]/gi, "").toUpperCase().slice(0, 6) || "FILE";
    return { base, ext };
}

function formatDate(unixTimestamp) {
    if (!unixTimestamp) return "-";
    const date = new Date(unixTimestamp * 1000);
    return date.toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: 'numeric' });
}

function formatBytes(bytes) {
    if (bytes === 0) return '0 B';
    const k = 1024;
    const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i];
}

function showDownloadProgress(percent) {
    if (!downloadProgressEl || !downloadProgressFillEl) return;

    const value = Number(percent);
    if (!Number.isFinite(value)) return;

    const clamped = Math.max(0, Math.min(100, value));

    if (downloadProgressHideTimeout) {
        clearTimeout(downloadProgressHideTimeout);
        downloadProgressHideTimeout = null;
    }

    downloadProgressEl.style.display = "block";
    downloadProgressEl.setAttribute("aria-valuenow", String(Math.round(clamped)));
    downloadProgressFillEl.style.width = `${clamped}%`;
}

function hideDownloadProgress() {
    if (!downloadProgressEl || !downloadProgressFillEl) return;

    if (downloadProgressHideTimeout) {
        clearTimeout(downloadProgressHideTimeout);
        downloadProgressHideTimeout = null;
    }

    downloadProgressEl.style.display = "none";
    downloadProgressEl.setAttribute("aria-valuenow", "0");
    downloadProgressFillEl.style.width = "0%";
}

function setupDownloadProgress() {
    downloadProgressEl = document.getElementById("transfer-progress");
    downloadProgressFillEl = document.getElementById("transfer-progress-fill");
    if (!downloadProgressEl || !downloadProgressFillEl) return;

    if (!window.runtime?.EventsOn) return;

    window.runtime.EventsOn("download_progress", (percent) => {
        if (activeTransfer !== "download") return;
        const value = Number(percent);
        if (!Number.isFinite(value)) return;

        const clamped = Math.max(0, Math.min(100, value));
        showDownloadProgress(clamped);

        const status = document.getElementById("status-msg");
        if (status) status.innerText = `Downloading… ${Math.round(clamped)}%`;

        if (clamped >= 100) {
            if (downloadProgressHideTimeout) clearTimeout(downloadProgressHideTimeout);
            downloadProgressHideTimeout = setTimeout(() => {
                hideDownloadProgress();
            }, 900);
        }
    });
}

function setupUploadProgress() {
    if (!window.runtime?.EventsOn) return;

    transferPillEl = document.getElementById("transfer-pill");
    transferSheetEl = document.getElementById("transfer-sheet");
    transferUploadListEl = document.getElementById("transfer-upload-list");
    transferClearEl = document.getElementById("transfer-clear");

    if (transferPillEl) {
        transferPillEl.addEventListener("click", () => {
            if (!transferSheetEl) return;
            if ((!uploadTransfers || uploadTransfers.size === 0) && !uploadBatch) return;
            const isOpen = transferSheetEl.style.display !== "none";
            transferSheetEl.style.display = isOpen ? "none" : "block";
            transferPillEl.setAttribute("aria-expanded", isOpen ? "false" : "true");
        });
    }

    document.addEventListener("click", (e) => {
        if (!transferSheetEl || transferSheetEl.style.display === "none") return;
        if (transferPillEl && transferPillEl.contains(e.target)) return;
        if (transferSheetEl.contains(e.target)) return;
        transferSheetEl.style.display = "none";
        if (transferPillEl) transferPillEl.setAttribute("aria-expanded", "false");
    });

    document.addEventListener("keydown", (e) => {
        if (e.key !== "Escape") return;
        if (!transferSheetEl || transferSheetEl.style.display === "none") return;
        transferSheetEl.style.display = "none";
        if (transferPillEl) transferPillEl.setAttribute("aria-expanded", "false");
    });

    if (transferClearEl) {
        transferClearEl.addEventListener("click", () => {
            uploadTransfers = new Map();
            uploadBatch = null;
            if (transferUploadListEl) transferUploadListEl.innerHTML = "";
            updateTransferPill();
            if (transferSheetEl) transferSheetEl.style.display = "none";
            if (transferPillEl) transferPillEl.setAttribute("aria-expanded", "false");
        });
    }

    window.runtime.EventsOn("upload_start", (id, name, size, parentId) => {
        const uploadId = Number(id);
        if (!Number.isFinite(uploadId)) return;
        const filename = String(name ?? "");

        const existing = uploadTransfers.get(uploadId);
        if (existing) {
            existing.name = existing.name || filename;
            existing.size = Number(size) || existing.size || 0;
            existing.parentId = String(parentId ?? existing.parentId ?? "");
            existing.state = "uploading";
            existing.progress = Math.max(0, Math.min(100, Number(existing.progress) || 0));
        } else {
            uploadTransfers.set(uploadId, {
                id: uploadId,
                name: filename,
                size: Number(size) || 0,
                parentId: String(parentId ?? ""),
                progress: 0,
                state: "uploading",
            });
        }

        renderTransferItem(uploadId);
        updateTransferPill();
    });

    window.runtime.EventsOn("upload_progress", (id, percent) => {
        const uploadId = Number(id);
        if (!Number.isFinite(uploadId)) return;
        const value = Number(percent);
        if (!Number.isFinite(value)) return;
        const clamped = Math.max(0, Math.min(100, value));

        const item = uploadTransfers.get(uploadId);
        if (!item) return;
        item.progress = clamped;
        renderTransferItem(uploadId);
    });

    window.runtime.EventsOn("upload_complete", (id, name) => {
        const uploadId = Number(id);
        if (!Number.isFinite(uploadId)) return;
        const item = uploadTransfers.get(uploadId);
        if (!item) {
            uploadTransfers.set(uploadId, {
                id: uploadId,
                name: String(name ?? ""),
                size: 0,
                parentId: "",
                progress: 100,
                state: "done",
            });
        } else {
            item.progress = 100;
            item.state = "done";
        }

        if (uploadBatch) uploadBatch.done += 1;
        renderTransferItem(uploadId);
        updateTransferPill();

        if (uploadBatch && uploadBatch.done + uploadBatch.failed >= uploadBatch.total) {
            uploadBatch = null;
            window.refreshFiles();
        }
    });

    window.runtime.EventsOn("upload_error", (id, name) => {
        const uploadId = Number(id);
        if (!Number.isFinite(uploadId)) return;
        const filename = String(name ?? "");

        const item = uploadTransfers.get(uploadId) || {
            id: uploadId,
            name: filename,
            size: 0,
            parentId: "",
            progress: 0,
            state: "failed",
        };
        item.state = "failed";
        item.progress = 100;
        uploadTransfers.set(uploadId, item);

        if (uploadBatch) uploadBatch.failed += 1;
        renderTransferItem(uploadId);
        updateTransferPill();

        if (uploadBatch && uploadBatch.done + uploadBatch.failed >= uploadBatch.total) {
            uploadBatch = null;
            window.refreshFiles();
        }
    });

    updateTransferPill();
}

function updateTransferPill() {
    if (!transferPillEl) return;
    const hasTransfers = uploadTransfers && uploadTransfers.size > 0;
    const hasBatch = Boolean(uploadBatch);

    transferPillEl.style.display = "inline-flex";
    transferPillEl.classList.toggle("is-idle", !hasTransfers && !hasBatch);
    transferPillEl.classList.toggle("is-active", hasBatch);

    if (!hasTransfers && !hasBatch) {
        transferPillEl.classList.remove("is-error");
        transferPillEl.innerHTML = `<span class="transfer-pill-dot" aria-hidden="true"></span><span class="transfer-pill-label"></span>`;
        if (transferClearEl) transferClearEl.style.display = "none";
        if (transferUploadListEl) transferUploadListEl.innerHTML = "";
        if (transferSheetEl) transferSheetEl.style.display = "none";
        transferPillEl.setAttribute("aria-expanded", "false");
        return;
    }

    let total = uploadBatch?.total ?? uploadTransfers.size;
    let done = uploadBatch?.done ?? 0;
    let failed = uploadBatch?.failed ?? 0;

    if (!uploadBatch) {
        done = 0;
        failed = 0;
        for (const item of uploadTransfers.values()) {
            if (item.state === "done") done += 1;
            if (item.state === "failed") failed += 1;
        }
        total = uploadTransfers.size;
    }

    const hasFailures = failed > 0;
    transferPillEl.classList.toggle("is-error", hasFailures);

    const label = uploadBatch ? `Uploading ${done}/${total}` : hasFailures ? `Uploads finished (${failed} failed)` : "Uploads";
    transferPillEl.innerHTML = `<span class="transfer-pill-dot" aria-hidden="true"></span><span class="transfer-pill-label">${escapeHtml(label)}</span>`;

    const allDone = done + failed >= total && total > 0;
    if (transferClearEl) transferClearEl.style.display = allDone ? "inline-flex" : "none";
}

function renderTransferItem(uploadId) {
    if (!transferUploadListEl) return;
    const item = uploadTransfers.get(uploadId);
    if (!item) return;

    let el = transferUploadListEl.querySelector(`.transfer-item[data-id="${uploadId}"]`);
    if (!el) {
        el = document.createElement("div");
        el.className = "transfer-item";
        el.dataset.id = String(uploadId);
        el.innerHTML = `
            <div class="transfer-item-fill"></div>
            <div class="transfer-item-content">
                <div class="transfer-item-name"></div>
                <div class="transfer-item-meta"></div>
            </div>
        `;
        transferUploadListEl.appendChild(el);
    }

    const fill = el.querySelector(".transfer-item-fill");
    const nameEl = el.querySelector(".transfer-item-name");
    const metaEl = el.querySelector(".transfer-item-meta");

    const progress = Math.max(0, Math.min(100, Number(item.progress) || 0));
    if (fill) fill.style.width = `${progress}%`;
    if (nameEl) nameEl.textContent = item.name || "Untitled";

    let meta = `${Math.round(progress)}%`;
    if (item.state === "queued") meta = "Queued";
    if (item.state === "done") meta = "Done";
    if (item.state === "failed") meta = "Failed";
    if (metaEl) metaEl.textContent = meta;

    el.classList.toggle("is-done", item.state === "done");
    el.classList.toggle("is-failed", item.state === "failed");
    el.classList.toggle("is-queued", item.state === "queued");
}

async function deleteFolder(folderID) {
    if (window.go?.main?.App?.DeleteFolder) {
        return window.go.main.App.DeleteFolder(folderID);
    }
    throw new Error("DeleteFolder is not available. Restart `wails dev` to regenerate bindings.");
}

async function uploadWithParentID(parentID) {
    const paths = await SelectFiles();
    if (!paths || !paths.length) return;

    activeTransfer = "upload";
    uploadBatch = { total: paths.length, done: 0, failed: 0 };

    const nextTransfers = new Map();
    for (let i = 0; i < paths.length; i++) {
        const p = String(paths[i] ?? "");
        const name = p ? p.split(/[/\\\\]/).pop() : "Untitled";
        nextTransfers.set(i, {
            id: i,
            name,
            size: 0,
            parentId: String(parentID || ""),
            progress: 0,
            state: "queued",
        });
    }
    uploadTransfers = nextTransfers;
    if (transferUploadListEl) transferUploadListEl.innerHTML = "";
    for (let i = 0; i < paths.length; i++) renderTransferItem(i);
    updateTransferPill();

    const upload = window?.go?.main?.App?.UploadToDriveFS;
    if (typeof upload !== "function") {
        activeTransfer = null;
        uploadBatch = null;
        uploadTransfers = new Map();
        if (transferUploadListEl) transferUploadListEl.innerHTML = "";
        updateTransferPill();
        alert("UploadToDriveFS is missing in backend. Rebuild the app (wails dev/build) and try again.");
        return;
    }

    try {
        const parentIDs = paths.map(() => parentID || "");
        await upload(paths, parentIDs);
    } catch (err) {
        console.error("Upload failed:", err);
    } finally {
        if (activeTransfer === "upload") activeTransfer = null;
    }
}

function ensureNotInsideDeletedFolder(deletedFolderID) {
    if (!deletedFolderID) return;

    const idx = folderPath.findIndex((f) => f.id === deletedFolderID);
    if (idx === -1) return;

    folderPath = folderPath.slice(0, idx);
    currentFolderId = folderPath.length ? folderPath[folderPath.length - 1].id : "";
    renderBreadcrumb();
}

function openDeleteModal(target) {
    const modal = document.getElementById("delete-modal");
    const title = document.getElementById("delete-modal-title");
    const subtitle = document.getElementById("delete-modal-subtitle");
    const confirmBtn = document.getElementById("delete-confirm");

    if (!modal || !title || !subtitle || !confirmBtn) return;

    pendingDeleteTarget = target;

    const name = (target?.name || "").trim();

    if (target?.type === "folder") {
        title.textContent = name ? `Delete folder “${name}”?` : "Delete folder?";
        subtitle.textContent = "This will permanently delete this folder and everything inside it from your Telegram channel.";
        confirmBtn.textContent = "Delete folder";
    } else {
        title.textContent = name ? `Delete “${name}”?` : "Delete file?";
        subtitle.textContent = "This will permanently delete the file from your Telegram channel.";
        confirmBtn.textContent = "Delete file";
    }

    modal.style.display = "flex";
}

function openRenameModal(target) {
    const modal = document.getElementById("rename-modal");
    const title = document.getElementById("rename-modal-title");
    const subtitle = document.getElementById("rename-modal-subtitle");
    const input = document.getElementById("rename-input");
    const errorEl = document.getElementById("rename-error");

    if (!modal || !title || !subtitle || !input) return;

    pendingRenameTarget = target;
    if (errorEl) {
        errorEl.innerText = "";
        errorEl.style.display = "none";
    }

    const isFolder = target?.type === "folder";
    title.textContent = isFolder ? "Rename folder" : "Rename file";
    subtitle.textContent = isFolder ? "Choose a new folder name." : "Choose a new file name.";

    input.value = String(target?.name || "");
    modal.style.display = "flex";

    requestAnimationFrame(() => {
        input.focus();
        const value = input.value || "";
        const dot = value.lastIndexOf(".");
        if (!isFolder && dot > 0 && dot < value.length - 1) {
            input.setSelectionRange(0, dot);
        } else {
            input.select();
        }
    });
}

async function ensureFileInTdriveSystem(target) {
    if (!target || target.type !== "file") return;
    if (String(target.source || "fs") !== "tg") return;

    const res = await MsgToTdriveSystem(
        Number(target.id),
        String(target.name || ""),
        Number(target.size || 0),
        String(target.parentId || "")
    );

    if (typeof res === "string" && res.startsWith("Error")) {
        throw new Error(res);
    }
}

function setupRenameModal() {
    const modal = document.getElementById("rename-modal");
    const cancelBtn = document.getElementById("rename-cancel");
    const confirmBtn = document.getElementById("rename-confirm");
    const input = document.getElementById("rename-input");
    const errorEl = document.getElementById("rename-error");

    if (!modal || !cancelBtn || !confirmBtn || !input) return;

    const showError = (message) => {
        if (!errorEl) return;
        errorEl.innerText = message || "";
        errorEl.style.display = message ? "block" : "none";
    };

    const close = () => {
        showError("");
        modal.style.display = "none";
        pendingRenameTarget = null;
    };

    cancelBtn.addEventListener("click", close);
    modal.addEventListener("click", (e) => {
        if (e.target === modal) close();
    });

    input.addEventListener("keydown", (e) => {
        if (e.key === "Enter") {
            e.preventDefault();
            confirmBtn.click();
        }
        if (e.key === "Escape") {
            e.preventDefault();
            close();
        }
    });

    confirmBtn.addEventListener("click", async () => {
        if (!pendingRenameTarget) return;
        const nextName = (input.value || "").trim();
        if (!nextName) {
            showError("Name can’t be empty.");
            return;
        }
        if (/[\\/]/.test(nextName)) {
            showError("Name can’t include / or \\.");
            return;
        }

        showError("");

        try {
            let res = "";
            if (pendingRenameTarget.type === "folder") {
                res = await RenameFolder(String(pendingRenameTarget.id), nextName);
            } else {
                await ensureFileInTdriveSystem(pendingRenameTarget);
                res = await RenameFile(Number(pendingRenameTarget.id), nextName);
            }

            if (typeof res === "string" && res.startsWith("Error")) {
                showError(res);
                return;
            }
            close();
            window.refreshFiles();
        } catch (err) {
            showError(err?.message || String(err));
        }
    });
}

async function buildFolderIndex() {
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

function getParentPath(folderId, byId) {
    const parents = [];
    let cur = byId.get(folderId);
    let safety = 0;
    while (cur && safety < 50) {
        const pid = cur.parent_id || "";
        const parent = byId.get(pid);
        if (!parent) break;
        parents.push(parent.name || "");
        cur = parent;
        safety += 1;
    }
    parents.reverse();
    return ["My Cloud", ...parents].filter(Boolean).join(" / ");
}

function collectDescendants(folderId, children) {
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

async function openMoveModal(target) {
    const modal = document.getElementById("move-modal");
    const title = document.getElementById("move-modal-title");
    const subtitle = document.getElementById("move-modal-subtitle");
    const search = document.getElementById("move-search");
    const list = document.getElementById("move-list");
    const errorEl = document.getElementById("move-error");

    if (!modal || !title || !subtitle || !search || !list) return;

    pendingMoveTarget = target;
    if (errorEl) {
        errorEl.innerText = "";
        errorEl.style.display = "none";
    }

    title.textContent = "Move to";
    subtitle.textContent = target?.type === "folder" ? "Choose where to move this folder." : "Choose where to move this file.";

    search.value = "";
    search.oninput = null;
    list.innerHTML = `<div class="move-empty">Loading folders…</div>`;
    modal.style.display = "flex";

    let index;
    try {
        index = await buildFolderIndex();
    } catch {
        index = { folders: [], byId: new Map(), children: new Map() };
    }

    const sourceParent = String(target?.parentId || "");
    const movingFolderId = target?.type === "folder" ? String(target?.id || "") : "";
    const blocked = movingFolderId ? collectDescendants(movingFolderId, index.children) : new Set();
    if (movingFolderId) blocked.add(movingFolderId);

    const all = [];
    all.push({ id: "", name: "My Cloud", path: "Root" });
    index.folders.forEach((folder) => {
        all.push({
            id: folder.id,
            name: folder.name || "Folder",
            path: getParentPath(folder.id, index.byId),
        });
    });

    all.sort((a, b) => {
        if (a.id === "") return -1;
        if (b.id === "") return 1;
        const ap = `${a.path} / ${a.name}`.toLowerCase();
        const bp = `${b.path} / ${b.name}`.toLowerCase();
        return ap.localeCompare(bp);
    });

    const render = (q) => {
        const query = (q || "").trim().toLowerCase();
        const items = query
            ? all.filter((item) => (item.name || "").toLowerCase().includes(query) || (item.path || "").toLowerCase().includes(query))
            : all;

        list.innerHTML = "";
        if (!items.length) {
            list.innerHTML = `<div class="move-empty">No folders found.</div>`;
            return;
        }

        items.forEach((item) => {
            const btn = document.createElement("button");
            btn.type = "button";
            btn.className = "move-item";

            const isSameParent = item.id === sourceParent;
            const isBlocked = movingFolderId ? blocked.has(item.id) : false;
            const disabled = isSameParent || isBlocked;
            if (disabled) btn.disabled = true;

            let pathText = item.path;
            if (isSameParent) pathText = "Current";
            if (isBlocked) pathText = "Not allowed";

            btn.innerHTML = `
                <span class="move-item-left">
                    <span class="move-item-icon" aria-hidden="true">${icons.folder}</span>
                    <span class="move-item-name">${escapeHtml(item.name)}</span>
                </span>
                <span class="move-item-path">${escapeHtml(pathText)}</span>
            `;

            btn.addEventListener("click", async () => {
                if (!pendingMoveTarget) return;
                if (errorEl) {
                    errorEl.innerText = "";
                    errorEl.style.display = "none";
                }

                try {
                    let res = "";
                    if (pendingMoveTarget.type === "folder") {
                        res = await MoveFolder(String(pendingMoveTarget.id), String(item.id));
                    } else {
                        await ensureFileInTdriveSystem(pendingMoveTarget);
                        res = await MoveFile(Number(pendingMoveTarget.id), String(item.id));
                    }

                    if (typeof res === "string" && res.startsWith("Error")) {
                        if (errorEl) {
                            errorEl.innerText = res;
                            errorEl.style.display = "block";
                        }
                        return;
                    }

                    const moveModal = document.getElementById("move-modal");
                    if (moveModal) moveModal.style.display = "none";
                    pendingMoveTarget = null;
                    window.refreshFiles();
                } catch (err) {
                    if (errorEl) {
                        errorEl.innerText = err?.message || String(err);
                        errorEl.style.display = "block";
                    }
                }
            });

            list.appendChild(btn);
        });
    };

    render("");
    search.oninput = () => render(search.value);

    requestAnimationFrame(() => {
        search.focus();
    });
}

function setupMoveModal() {
    const modal = document.getElementById("move-modal");
    const cancelBtn = document.getElementById("move-cancel");
    const list = document.getElementById("move-list");
    const search = document.getElementById("move-search");
    const errorEl = document.getElementById("move-error");

    if (!modal || !cancelBtn) return;

    const close = () => {
        modal.style.display = "none";
        pendingMoveTarget = null;
        if (list) list.innerHTML = "";
        if (search) {
            search.value = "";
            search.oninput = null;
        }
        if (errorEl) {
            errorEl.innerText = "";
            errorEl.style.display = "none";
        }
    };

    cancelBtn.addEventListener("click", close);
    modal.addEventListener("click", (e) => {
        if (e.target === modal) close();
    });
}

window.onload = async function() {
    console.log("App loaded. Checking Status...");
    setupDeleteModal();
    setupFolderModal();
    setupRenameModal();
	    setupMoveModal();
	    setupBreadcrumb();
	    setupContextMenu();
	    setupDownloadProgress();
	    setupUploadProgress();
	    setupPasswordReveal();

    try {
        // Step A: Check Setup
        // If this fails, it's because Wails bindings are missing. Restart Wails!
        let status = await CheckSystemStatus();
        
        if (status === "NEEDS_SETUP") {
            showAuthWrapper();
            hideAllScreens();
            document.getElementById("setupcontainer").style.display = "block";
            return;
        }

        // Step B: Check Login
        let isLoggedIn = await CheckLoginStatus();
        if (isLoggedIn) {
            showDashboard();
        } else {
            // Ensure login screen is visible if not logged in
            showAuthWrapper();
            hideAllScreens();
            document.getElementById("phonecontainer").style.display = "block";
        }

    } catch (err) {
        console.error("Startup Crash:", err);
        // Don't hide everything if we crash. Let the user see the console error.
        alert("Startup Error: " + err + "\n\nDid you restart 'wails dev'?");
    }
};

function hideAllScreens() {
    const screens = ["setupcontainer", "phonecontainer", "codecontainer", "passwordcontainer", "success-screen"];
    screens.forEach(id => {
        const el = document.getElementById(id);
        if(el) el.style.display = "none";
    });
}

function showAuthWrapper() {
    const authWrapper = document.getElementById("auth-wrapper");
    if (authWrapper) authWrapper.style.display = "flex";

    const dashboard = document.getElementById("success-screen");
    if (dashboard) dashboard.style.display = "none";
}

function setupPasswordReveal() {
    const pw = document.getElementById("enterpassword");
    const toggle = document.getElementById("toggle-password");
    if (!pw || !toggle) return;

    const apply = (isVisible) => {
        pw.type = isVisible ? "text" : "password";

        toggle.dataset.state = isVisible ? "visible" : "hidden";
        toggle.setAttribute("aria-label", isVisible ? "Hide password" : "Show password");
        toggle.setAttribute("title", isVisible ? "Hide password" : "Show password");
    };

    apply(false);

    toggle.addEventListener("click", () => {
        const isVisible = pw.type === "password";
        apply(isVisible);
        pw.focus();
    });
}

function setupDeleteModal() {
    const modal = document.getElementById("delete-modal");
    const cancelBtn = document.getElementById("delete-cancel");
    const confirmBtn = document.getElementById("delete-confirm");

    if (!modal || !cancelBtn || !confirmBtn) return;

    const close = () => {
        pendingDeleteTarget = null;
        modal.style.display = "none";
    };

    cancelBtn.addEventListener("click", close);
    modal.addEventListener("click", (e) => {
        if (e.target === modal) close();
    });

    confirmBtn.addEventListener("click", () => {
        const target = pendingDeleteTarget;
        close();
        if (!target) return;

        const status = document.getElementById("status-msg");
        if (status) status.innerText = "Deleting...";

        const promise = target.type === "folder"
            ? deleteFolder(String(target.id))
            : DeleteFile(Number(target.id));

        promise
            .then((res) => {
                if (target.type === "folder") ensureNotInsideDeletedFolder(String(target.id));
                if (status) status.innerText = res || "Done";
                window.refreshFiles();
            })
            .catch((err) => {
                console.error("Delete failed:", err);
                if (status) status.innerText = "Delete failed";
                alert("Delete failed. Check console/logs.");
            })
            .finally(() => {
                setTimeout(() => {
                    if (status) status.innerText = "Ready";
                }, 2000);
            });
    });
}

function setupFolderModal() {
    const modal = document.getElementById("folder-modal");
    const cancelBtn = document.getElementById("folder-cancel");
    const createBtn = document.getElementById("folder-create");
    const nameInput = document.getElementById("new-folder-name");

    if (!modal || !cancelBtn || !createBtn || !nameInput) return;

    const close = () => {
        modal.style.display = "none";
        nameInput.value = "";
    };

    cancelBtn.addEventListener("click", close);
    modal.addEventListener("click", (e) => {
        if (e.target === modal) close();
    });

    const submit = async () => {
        const name = (nameInput.value || "").trim();
        if (!name) return;

        const status = document.getElementById("status-msg");
        if (status) status.innerText = "Creating folder...";

        try {
            await createFolder(name, currentFolderId);
            close();
            window.refreshFiles();
        } catch (err) {
            alert("Failed to create folder: " + err);
        } finally {
            if (status) status.innerText = "Ready";
        }
    };

    createBtn.addEventListener("click", submit);
    nameInput.addEventListener("keydown", (e) => {
        if (e.key === "Enter") submit();
        if (e.key === "Escape") close();
    });
}

window.openNewFolderModal = function() {
    const modal = document.getElementById("folder-modal");
    const nameInput = document.getElementById("new-folder-name");
    if (!modal || !nameInput) return;

    modal.style.display = "flex";
    setTimeout(() => nameInput.focus(), 0);
};

function setupBreadcrumb() {
    const backBtn = document.getElementById("breadcrumb-back");
    const path = document.getElementById("breadcrumb-path");
    if (!backBtn || !path) return;

    backBtn.addEventListener("click", () => {
        if (folderPath.length === 0) return;
        folderPath = folderPath.slice(0, -1);
        currentFolderId = folderPath.length ? folderPath[folderPath.length - 1].id : "";
        renderBreadcrumb();
        window.refreshFiles();
    });

    path.addEventListener("click", (e) => {
        const btn = e.target.closest("button.breadcrumb-link");
        if (!btn) return;
        const idx = parseInt(btn.dataset.index, 10);
        if (Number.isNaN(idx)) return;

        if (idx < 0) {
            folderPath = [];
            currentFolderId = "";
        } else {
            folderPath = folderPath.slice(0, idx + 1);
            currentFolderId = folderPath[idx]?.id || "";
        }
        renderBreadcrumb();
        window.refreshFiles();
    });

    renderBreadcrumb();
}

function renderBreadcrumb() {
    const backBtn = document.getElementById("breadcrumb-back");
    const path = document.getElementById("breadcrumb-path");
    if (!backBtn || !path) return;

    backBtn.disabled = folderPath.length === 0;
    backBtn.style.opacity = folderPath.length === 0 ? "0.35" : "1";

    const items = [{ name: "My Drive", index: -1 }, ...folderPath.map((f, i) => ({ name: f.name, index: i }))];
    path.innerHTML = "";

    items.forEach((item, idx) => {
        if (idx > 0) {
            const sep = document.createElement("span");
            sep.className = "breadcrumb-sep";
            sep.textContent = "/";
            path.appendChild(sep);
        }

        const btn = document.createElement("button");
        btn.type = "button";
        btn.className = "breadcrumb-link";
        btn.dataset.index = String(item.index);
        btn.textContent = item.name;
        path.appendChild(btn);
    });
}

function setupContextMenu() {
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

	        if (type === "folder") {
	            const folderID = row.dataset.id;
	            const folderName = row.dataset.name || "Folder";
	            show(e.clientX, e.clientY, [
	                { label: `Open "${folderName}"`, onClick: () => navigateToFolder(folderID, folderName) },
	                { label: "Upload to this folder", onClick: () => uploadWithParentID(folderID) },
	                { label: "Rename…", onClick: () => openRenameModal({ type: "folder", id: folderID, name: folderName, parentId: currentFolderId }) },
	                { label: "Move to…", onClick: () => openMoveModal({ type: "folder", id: folderID, name: folderName, parentId: currentFolderId }) },
	                { label: `Delete "${folderName}"`, danger: true, onClick: () => window.initDeleteFolder(folderID, folderName) },
	                { type: "divider" },
	                { label: "New folder", onClick: () => window.openNewFolderModal() },
	                { label: "Refresh", onClick: () => window.refreshFiles() },
            ]);
            return;
        }

	        if (type === "file") {
	            const fileID = parseInt(row.dataset.id, 10);
	            const fileName = row.dataset.name || "";
	            const fileSource = row.dataset.source || "fs";
	            const canDelete = row.dataset.canDelete === "true";
	            const items = [
                { label: "Download", onClick: () => window.initDownload(fileID) },
            ];
            if (fileSource === "fs") {
                items.push(
                    { label: "Rename…", onClick: () => openRenameModal({ type: "file", id: fileID, name: fileName, parentId: currentFolderId }) },
                    { label: "Move to…", onClick: () => openMoveModal({ type: "file", id: fileID, name: fileName, parentId: currentFolderId }) },
                );
            } else {
                const fileSize = Number(row.dataset.size || 0);
                items.push(
                    { label: "Rename…", onClick: () => openRenameModal({ type: "file", id: fileID, name: fileName, size: fileSize, parentId: currentFolderId, source: "tg" }) },
                    { label: "Move to…", onClick: () => openMoveModal({ type: "file", id: fileID, name: fileName, size: fileSize, parentId: currentFolderId, source: "tg" }) },
                );
            }
            if (canDelete) {
                items.push({ label: "Delete", danger: true, onClick: () => window.initDelete(fileID, fileName) });
            }
	            items.push(
                { type: "divider" },
                { label: "Upload", onClick: () => window.selectFile() },
                { label: "New folder", onClick: () => window.openNewFolderModal() },
                { label: "Refresh", onClick: () => window.refreshFiles() },
            );
            show(e.clientX, e.clientY, items);
            return;
        }

        show(e.clientX, e.clientY, [
            { label: "New folder", onClick: () => window.openNewFolderModal() },
            { label: "Upload", onClick: () => window.selectFile() },
            { label: "Refresh", onClick: () => window.refreshFiles() },
        ]);
    });
}

function navigateToFolder(folderID, folderName) {
    folderPath = [...folderPath, { id: folderID, name: folderName }];
    currentFolderId = folderID;
    renderBreadcrumb();
    window.refreshFiles();
}

async function getFolderContents(parentID) {
    if (window.go?.main?.App?.GetFolderContents) {
        return window.go.main.App.GetFolderContents(parentID);
    }
    throw new Error("GetFolderContents is not available. Restart `wails dev` to regenerate bindings.");
}

async function createFolder(name, parentID) {
    if (window.go?.main?.App?.CreateFolder) {
        return window.go.main.App.CreateFolder(name, parentID);
    }
    throw new Error("CreateFolder is not available. Restart `wails dev` to regenerate bindings.");
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

window.submitSetup = function() {
    const id = parseInt(document.getElementById("api_id").value);
    const hash = document.getElementById("api_hash").value;
    if (!id || !hash) return alert("Enter both fields.");

    SaveSetup(id, hash).then(res => {
        if(res === "Success") location.reload();
        else alert(res);
    });
};

window.startLogin = function () {
    const phone = (document.getElementById("enterphone").value || "").trim();
    if(!phone) return alert("Enter phone number");

    lastLoginPhoneNumber = phone;
    
    LoginPhoneNumber(phone).then(() => {
        showAuthWrapper();
        hideAllScreens();
        document.getElementById("codecontainer").style.display = "block";

        const row = document.getElementById("code-target-row");
        const target = document.getElementById("code-target");
        if (target) target.innerText = lastLoginPhoneNumber;
        if (row) row.style.display = lastLoginPhoneNumber ? "flex" : "none";
    });
};

window.sendCode = function () {
    const code = document.getElementById("entercode").value;
    SumbitCode(code).then(() => {
        showAuthWrapper();
        hideAllScreens();
        document.getElementById("passwordcontainer").style.display = "block";

        const hintBox = document.getElementById("hint-box");
        const hintEl = document.getElementById("hinttext");
        if (hintEl) hintEl.innerText = "";
        if (hintBox) hintBox.style.display = "none";

        const pw = document.getElementById("enterpassword");
        const toggle = document.getElementById("toggle-password");
        if (pw) pw.type = "password";
        if (toggle) {
            toggle.dataset.state = "hidden";
            toggle.setAttribute("aria-label", "Show password");
            toggle.setAttribute("title", "Show password");
        }
    });
};

window.sendPassword = function () {
    SumbitPassword(document.getElementById("enterpassword").value);
};

window.runtime.EventsOn("login-success", () => showDashboard());

window.runtime.EventsOn("gothint", (hint) => {
    const hintEl = document.getElementById("hinttext");
    const hintBox = document.getElementById("hint-box");
    if (!hintEl || !hintBox) return;

    const text = (hint ?? "").toString().trim();
    const normalized = text.replace(/^(hint\s*:?[\s\u00A0]*)+/i, "").trim();

    if (!normalized || normalized.toLowerCase().includes("no hint")) {
        hintEl.innerText = "";
        hintBox.style.display = "none";
        return;
    }

    hintEl.innerText = normalized;
    hintBox.style.display = "block";
});

window.backToPhone = function () {
    showAuthWrapper();
    hideAllScreens();

    const phoneContainer = document.getElementById("phonecontainer");
    if (phoneContainer) phoneContainer.style.display = "block";

    const codeEl = document.getElementById("entercode");
    if (codeEl) codeEl.value = "";
};

async function initDriveWithRetry(maxAttempts = 3) {
    let lastError = "Error: Init failed";

    for (let attempt = 1; attempt <= maxAttempts; attempt++) {
        const status = document.getElementById("status-msg");
        if (status) status.innerText = `Setting up… (${attempt}/${maxAttempts})`;

        try {
            const initRes = await InitDrive();

            if (typeof initRes === "string" && initRes.startsWith("Error:")) {
                lastError = initRes;
                console.error("InitDrive error:", initRes);

                if (initRes.includes("could not save config")) {
                    break;
                }
            } else {
                return { ok: true, message: initRes };
            }
        } catch (err) {
            lastError = "Error: " + (err?.message || String(err));
            console.error("InitDrive failed:", err);
        }

        if (attempt < maxAttempts) {
            await new Promise((resolve) => setTimeout(resolve, 700 * attempt));
        }
    }

    return { ok: false, error: lastError };
}

async function showDashboard() {
    const authWrapper = document.getElementById("auth-wrapper");
    if (authWrapper) authWrapper.style.display = "none";

    hideAllScreens();
    document.getElementById("success-screen").style.display = "flex";
    currentFolderId = "";
    folderPath = [];
    renderBreadcrumb();

    const initResult = await initDriveWithRetry(3);
    const status = document.getElementById("status-msg");

    if (!initResult.ok) {
        if (status) status.innerText = "Init failed";
        alert(initResult.error || "Failed to initialize your drive. Check logs/console and try again.");
        return;
    }

    if (status) status.innerText = "Ready";
    window.refreshFiles();
}

window.refreshFiles = function() {
    const list = document.getElementById("file-list");
    const storageUsed = document.getElementById("storage-used");
    const requestedFolderId = currentFolderId;

    list.innerHTML = '<div style="padding:20px; color:#565f89;">Loading...</div>';
    if (storageUsed) storageUsed.innerText = "Calculating... / Unlimited";

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

            if (currentFolderId !== requestedFolderId) return;

            if (storageUsed) {
                const totalBytes = files.reduce((sum, f) => sum + (f?.size || 0), 0);
                storageUsed.innerText = `${formatBytes(totalBytes)} / Unlimited`;
            }

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

                row.innerHTML = `
                    <div class="row-name">
                        <span class="folder-chip" aria-hidden="true">${icons.folder}</span>
                        ${escapeHtml(folder.name)}
                    </div>
                    <div class="row-meta">—</div>
                    <div class="row-meta">—</div>
                    <div class="row-actions">
                        <button class="action-icon open-folder" type="button" title="Open">${icons.open}</button>
                        <button class="action-icon del delete-folder" type="button" title="Delete folder">${icons.trash}</button>
                    </div>
                `;

                row.addEventListener("dblclick", () => navigateToFolder(folder.id, folder.name));
                row.addEventListener("click", (e) => {
                    if (e.target.closest("button.open-folder")) {
                        navigateToFolder(folder.id, folder.name);
                    }
                    if (e.target.closest("button.delete-folder")) {
                        window.initDeleteFolder(folder.id, folder.name);
                    }
                });

                list.appendChild(row);
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
                        <button class="action-icon del delete" type="button" title="Delete">${icons.trash}</button>
                    </div>
                `;

                const downloadBtn = row.querySelector("button.download");
                if (downloadBtn) {
                    downloadBtn.addEventListener("click", () => window.initDownload(file.id));
                }
	                const deleteBtn = row.querySelector("button.delete");
	                if (deleteBtn) {
	                    deleteBtn.addEventListener("click", () => window.initDelete(file.id, file.name));
	                }

		                const nameEl = row.querySelector(".row-name");
		                if (nameEl) {
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
		                            parentId: currentFolderId,
		                            source: file.source || row.dataset.source || "fs",
		                        });
		                    });
		                }
	                list.appendChild(row);
	            });
	        };

        finalize();
    });
};

window.selectFile = function() {
    uploadWithParentID(currentFolderId);
};

window.initDownload = function(id) {
    const status = document.getElementById("status-msg");
    activeTransfer = "download";
    if (status) status.innerText = "Downloading…";

    showDownloadProgress(0);

    DownloadFile(id)
        .then((res) => {
            alert(res);
        })
        .catch((err) => {
            console.error("Download failed:", err);
            alert("Download failed. Check console/logs.");
        })
        .finally(() => {
            if (activeTransfer === "download") activeTransfer = null;
            hideDownloadProgress();
            if (status) status.innerText = "Ready";
        });
};

window.initDeleteFolder = function(folderID, folderName) {
    openDeleteModal({ type: "folder", id: folderID, name: folderName || "" });
};

window.initDelete = function(id, name) {
    openDeleteModal({ type: "file", id, name: name || "" });
};
