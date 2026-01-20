
import { 
    CheckSystemStatus, SaveSetup,
    LoginPhoneNumber, SumbitCode, SumbitPassword, 
    GetFileList, DownloadFile, DeleteFile, 
    CheckLoginStatus, InitDrive, SelectFile
} from '../wailsjs/go/main/App';

let pendingDeleteTarget = null; // { type: "file" | "folder", id: number|string, name?: string }
let currentFolderId = "";
let folderPath = []; // [{ id, name }]
let downloadProgressEl = null;
let downloadProgressFillEl = null;
let downloadProgressHideTimeout = null;

const icons = {
    download: `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M4 16v1a3 3 0 003 3h10a3 3 0 003-3v-1m-4-4l-4 4m0 0l-4-4m4 4V4"/></svg>`,
    trash: `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16"/></svg>`,
    folder: `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path stroke-linecap="round" stroke-linejoin="round" d="M3 7a2 2 0 012-2h5l2 2h7a2 2 0 012 2v8a2 2 0 01-2 2H5a2 2 0 01-2-2V7z"/></svg>`,
    open: `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path stroke-linecap="round" stroke-linejoin="round" d="M9 5H7a2 2 0 00-2 2v10a2 2 0 002 2h10a2 2 0 002-2v-2"/><path stroke-linecap="round" stroke-linejoin="round" d="M14 3h7v7"/><path stroke-linecap="round" stroke-linejoin="round" d="M10 14L21 3"/></svg>`,
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

async function deleteFolder(folderID) {
    if (window.go?.main?.App?.DeleteFolder) {
        return window.go.main.App.DeleteFolder(folderID);
    }
    throw new Error("DeleteFolder is not available. Restart `wails dev` to regenerate bindings.");
}

async function uploadWithParentID(parentID) {
    const path = await SelectFile();
    if (!path) return;

    const status = document.getElementById("status-msg");
    if (status) status.innerText = "Uploading...";

    const upload = window?.go?.main?.App?.UploadToDriveFS;
    if (typeof upload !== "function") {
        if (status) status.innerText = "Ready";
        alert("UploadToDriveFS is missing in backend. Rebuild the app (wails dev/build) and try again.");
        return;
    }

    try {
        await upload(path, parentID || "");
        if (status) status.innerText = "Ready";
        window.refreshFiles();
    } catch (err) {
        console.error("Upload failed:", err);
        if (status) status.innerText = "Ready";
        alert("Upload failed. Check console/logs.");
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

window.onload = async function() {
    console.log("App loaded. Checking Status...");
    setupDeleteModal();
    setupFolderModal();
    setupBreadcrumb();
    setupContextMenu();
    setupDownloadProgress();

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
            const canDelete = row.dataset.canDelete === "true";
            const items = [
                { label: "Download", onClick: () => window.initDownload(fileID) },
            ];
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
    const phone = document.getElementById("enterphone").value;
    if(!phone) return alert("Enter phone number");
    
    LoginPhoneNumber(phone).then(() => {
        showAuthWrapper();
        hideAllScreens();
        document.getElementById("codecontainer").style.display = "block";
    });
};

window.sendCode = function () {
    const code = document.getElementById("entercode").value;
    SumbitCode(code).then(() => {
        showAuthWrapper();
        hideAllScreens();
        document.getElementById("passwordcontainer").style.display = "block";
    });
};

window.sendPassword = function () {
    SumbitPassword(document.getElementById("enterpassword").value);
};

window.runtime.EventsOn("login-success", () => showDashboard());

function showDashboard() {
    const authWrapper = document.getElementById("auth-wrapper");
    if (authWrapper) authWrapper.style.display = "none";

    hideAllScreens();
    document.getElementById("success-screen").style.display = "flex";
    currentFolderId = "";
    folderPath = [];
    renderBreadcrumb();
    InitDrive().then(() => window.refreshFiles());
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
