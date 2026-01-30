// Upload/Download progress handling for TDrive frontend

import { state } from '../state.js';
import { escapeHtml } from '../utils.js';
import { SelectFiles, DownloadFile } from '../../wailsjs/go/main/App';

export function showDownloadProgress(percent) {
    const value = Number(percent);
    if (!Number.isFinite(value)) return;
    const activeId = state.activeDownloadId;
    if (activeId === null || activeId === undefined) return;

    const item = (state.downloadQueue || []).find((entry) => entry.id === activeId);
    if (!item) return;

    const clamped = Math.max(0, Math.min(100, value));
    item.progress = clamped;
    item.state = "downloading";
    renderDownloadItem(item);
    updateTransferPill();

    if (state.downloadProgressHideTimeout) {
        clearTimeout(state.downloadProgressHideTimeout);
        state.downloadProgressHideTimeout = null;
    }

    if (state.downloadProgressEl && state.downloadProgressFillEl) {
        state.downloadProgressEl.style.display = "none";
        state.downloadProgressEl.setAttribute("aria-valuenow", String(Math.round(clamped)));
        state.downloadProgressFillEl.style.width = `${clamped}%`;
    }
}

export function hideDownloadProgress() {
    if (state.downloadProgressHideTimeout) {
        clearTimeout(state.downloadProgressHideTimeout);
        state.downloadProgressHideTimeout = null;
    }

    if (state.downloadProgressEl && state.downloadProgressFillEl) {
        state.downloadProgressEl.style.display = "none";
        state.downloadProgressEl.setAttribute("aria-valuenow", "0");
        state.downloadProgressFillEl.style.width = "0%";
    }
}

export function setupDownloadProgress() {
    state.downloadProgressEl = document.getElementById("transfer-progress");
    state.downloadProgressFillEl = document.getElementById("transfer-progress-fill");
    if (!window.runtime?.EventsOn) return;

    window.runtime.EventsOn("download_progress", (percent) => {
        if (!state.downloadQueue || state.activeDownloadId === null || state.activeDownloadId === undefined) return;
        const value = Number(percent);
        if (!Number.isFinite(value)) return;

        const clamped = Math.max(0, Math.min(100, value));
        showDownloadProgress(clamped);

        const status = document.getElementById("status-msg");
        if (status) status.innerText = `Downloading… ${Math.round(clamped)}%`;
    });
}

function getDownloadCounts() {
    const items = Array.isArray(state.downloadQueue) ? state.downloadQueue : [];
    let total = items.length;
    let done = 0;
    let failed = 0;
    let queued = 0;
    let downloading = 0;

    items.forEach((item) => {
        const status = String(item?.state || "queued");
        if (status === "done") done += 1;
        else if (status === "failed") failed += 1;
        else if (status === "downloading") downloading += 1;
        else queued += 1;
    });

    return { total, done, failed, queued, downloading };
}

export function updateTransferPill() {
    if (!state.transferPillEl) return;
    const hasUploads = state.uploadTransfers && state.uploadTransfers.size > 0;
    const hasBatch = Boolean(state.uploadBatch);
    const downloadCounts = getDownloadCounts();
    const hasDownloads = downloadCounts.total > 0;

    state.transferPillEl.style.display = "inline-flex";
    state.transferPillEl.classList.toggle("is-idle", !hasUploads && !hasBatch && !hasDownloads);
    state.transferPillEl.classList.toggle("is-active", hasBatch || downloadCounts.downloading > 0);

    if (!hasUploads && !hasBatch && !hasDownloads) {
        state.transferPillEl.classList.remove("is-error");
        state.transferPillEl.innerHTML = `<span class="transfer-pill-dot" aria-hidden="true"></span><span class="transfer-pill-label"></span>`;
        if (state.transferClearEl) state.transferClearEl.style.display = "none";
        if (state.transferUploadListEl) state.transferUploadListEl.innerHTML = "";
        if (state.transferSheetEl) state.transferSheetEl.style.display = "none";
        state.transferPillEl.setAttribute("aria-expanded", "false");
        return;
    }

    let total = state.uploadBatch?.total ?? state.uploadTransfers.size;
    let done = state.uploadBatch?.done ?? 0;
    let failed = state.uploadBatch?.failed ?? 0;

    if (!state.uploadBatch) {
        done = 0;
        failed = 0;
        for (const item of state.uploadTransfers.values()) {
            if (item.state === "done") done += 1;
            if (item.state === "failed") failed += 1;
        }
        total = state.uploadTransfers.size;
    }

    const uploadFailed = failed;
    const downloadFailed = downloadCounts.failed;
    const hasFailures = uploadFailed > 0 || downloadFailed > 0;
    state.transferPillEl.classList.toggle("is-error", hasFailures);

    let label = "";
    const uploadLabel = (hasUploads || hasBatch)
        ? (state.uploadBatch ? `Uploading ${done}/${total}` : uploadFailed > 0 ? `Uploads finished (${uploadFailed} failed)` : "Uploads")
        : "";

    let downloadLabel = "";
    if (hasDownloads) {
        if (downloadCounts.downloading > 0) {
            const current = Math.min(downloadCounts.done + downloadCounts.failed + 1, downloadCounts.total);
            downloadLabel = `Downloading ${current}/${downloadCounts.total}`;
        } else if (downloadCounts.queued > 0) {
            downloadLabel = `Downloads queued (${downloadCounts.queued})`;
        } else if (downloadFailed > 0) {
            downloadLabel = `Downloads finished (${downloadFailed} failed)`;
        } else {
            downloadLabel = "Downloads finished";
        }
    }

    if (uploadLabel && downloadLabel) label = `${uploadLabel} · ${downloadLabel}`;
    else label = uploadLabel || downloadLabel || "";

    state.transferPillEl.innerHTML = `<span class="transfer-pill-dot" aria-hidden="true"></span><span class="transfer-pill-label">${escapeHtml(label)}</span>`;

    const uploadsDone = !hasUploads ? true : (done + failed >= total && total > 0);
    const downloadsDone = !hasDownloads || downloadCounts.downloading === 0 && downloadCounts.queued === 0;
    const allDone = uploadsDone && downloadsDone;
    if (state.transferClearEl) state.transferClearEl.style.display = allDone ? "inline-flex" : "none";
}

export function renderTransferItem(uploadId) {
    if (!state.transferUploadListEl) return;
    const item = state.uploadTransfers.get(uploadId);
    if (!item) return;

    let el = state.transferUploadListEl.querySelector(`.transfer-item[data-type="upload"][data-id="${uploadId}"]`);
    if (!el) {
        el = document.createElement("div");
        el.className = "transfer-item";
        el.dataset.type = "upload";
        el.dataset.id = String(uploadId);
        el.innerHTML = `
            <div class="transfer-item-fill"></div>
            <div class="transfer-item-content">
                <div class="transfer-item-name"></div>
                <div class="transfer-item-meta"></div>
            </div>
        `;
        state.transferUploadListEl.appendChild(el);
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

export function renderDownloadItem(item) {
    if (!state.transferUploadListEl || !item) return;
    const list = state.transferUploadListEl;
    const id = Number(item.id);
    const selector = `.transfer-item[data-type="download"][data-id="${id}"]`;

    let el = list.querySelector(selector);
    if (!el) {
        el = document.createElement("div");
        el.className = "transfer-item";
        el.dataset.type = "download";
        el.dataset.id = String(id);
        el.innerHTML = `
            <div class="transfer-item-fill"></div>
            <div class="transfer-item-content">
                <div class="transfer-item-name"></div>
                <div class="transfer-item-meta"></div>
            </div>
        `;
        list.prepend(el);
    }

    const fill = el.querySelector(".transfer-item-fill");
    const nameEl = el.querySelector(".transfer-item-name");
    const metaEl = el.querySelector(".transfer-item-meta");

    const progress = Math.max(0, Math.min(100, Number(item.progress) || 0));
    if (fill) fill.style.width = `${progress}%`;
    if (nameEl) nameEl.textContent = item.name || "Download";

    let meta = `${Math.round(progress)}%`;
    if (item.state === "queued") meta = "Queued";
    if (item.state === "done") meta = "Done";
    if (item.state === "failed") meta = "Failed";
    if (metaEl) metaEl.textContent = meta;

    el.classList.toggle("is-done", item.state === "done");
    el.classList.toggle("is-failed", item.state === "failed");
    el.classList.toggle("is-queued", item.state === "queued");
}

async function startNextDownload() {
    if (state.activeDownloadId !== null && state.activeDownloadId !== undefined) return;
    const queue = Array.isArray(state.downloadQueue) ? state.downloadQueue : [];
    const next = queue.find((entry) => String(entry?.state || "queued") === "queued");
    if (!next) return;

    state.activeDownloadId = next.id;
    next.state = "downloading";
    next.progress = Math.max(0, Math.min(100, Number(next.progress) || 0));
    renderDownloadItem(next);
    updateTransferPill();

    const status = document.getElementById("status-msg");
    if (status) status.innerText = "Downloading…";

    try {
        const res = await DownloadFile(Number(next.id));
        alert(res);
        next.state = "done";
        next.progress = 100;
    } catch (err) {
        console.error("Download failed:", err);
        alert("Download failed. Check console/logs.");
        next.state = "failed";
        next.progress = 100;
    } finally {
        state.activeDownloadId = null;
        renderDownloadItem(next);
        updateTransferPill();
        hideDownloadProgress();
        if (status) status.innerText = "Ready";
        startNextDownload();
    }
}

export function enqueueDownload(id, name, size) {
    const downloadId = Number(id);
    if (!Number.isFinite(downloadId)) return;
    if (!Array.isArray(state.downloadQueue)) state.downloadQueue = [];

    const label = String(name || "Download");
    const existing = state.downloadQueue.find((entry) => entry.id === downloadId);
    if (existing) {
        existing.name = existing.name || label;
        existing.size = Number(size) || existing.size || 0;
        existing.progress = 0;
        existing.state = "queued";
        renderDownloadItem(existing);
        updateTransferPill();
        if (state.activeDownloadId === null || state.activeDownloadId === undefined) startNextDownload();
        return;
    }

    const item = {
        id: downloadId,
        name: label,
        size: Number(size) || 0,
        progress: 0,
        state: "queued",
    };

    state.downloadQueue.push(item);
    renderDownloadItem(item);
    updateTransferPill();
    if (state.activeDownloadId === null || state.activeDownloadId === undefined) startNextDownload();
}

export function setupUploadProgress() {
    if (!window.runtime?.EventsOn) return;

    state.transferPillEl = document.getElementById("transfer-pill");
    state.transferSheetEl = document.getElementById("transfer-sheet");
    state.transferUploadListEl = document.getElementById("transfer-upload-list");
    state.transferClearEl = document.getElementById("transfer-clear");

    if (state.transferPillEl) {
        state.transferPillEl.addEventListener("click", () => {
            if (!state.transferSheetEl) return;
            const hasUploads = state.uploadTransfers && state.uploadTransfers.size > 0;
            const hasBatch = Boolean(state.uploadBatch);
            const hasDownloads = Array.isArray(state.downloadQueue) && state.downloadQueue.length > 0;
            if (!hasUploads && !hasBatch && !hasDownloads) return;
            const isOpen = state.transferSheetEl.style.display !== "none";
            state.transferSheetEl.style.display = isOpen ? "none" : "block";
            state.transferPillEl.setAttribute("aria-expanded", isOpen ? "false" : "true");
        });
    }

    document.addEventListener("click", (e) => {
        if (!state.transferSheetEl || state.transferSheetEl.style.display === "none") return;
        if (state.transferPillEl && state.transferPillEl.contains(e.target)) return;
        if (state.transferSheetEl.contains(e.target)) return;
        state.transferSheetEl.style.display = "none";
        if (state.transferPillEl) state.transferPillEl.setAttribute("aria-expanded", "false");
    });

    document.addEventListener("keydown", (e) => {
        if (e.key !== "Escape") return;
        if (!state.transferSheetEl || state.transferSheetEl.style.display === "none") return;
        state.transferSheetEl.style.display = "none";
        if (state.transferPillEl) state.transferPillEl.setAttribute("aria-expanded", "false");
    });

    if (state.transferClearEl) {
        state.transferClearEl.addEventListener("click", () => {
            state.uploadTransfers = new Map();
            state.uploadBatch = null;
            state.downloadQueue = [];
            state.activeDownloadId = null;
            if (state.transferUploadListEl) state.transferUploadListEl.innerHTML = "";
            updateTransferPill();
            if (state.transferSheetEl) state.transferSheetEl.style.display = "none";
            if (state.transferPillEl) state.transferPillEl.setAttribute("aria-expanded", "false");
        });
    }

    window.runtime.EventsOn("upload_start", (id, name, size, parentId) => {
        const uploadId = Number(id);
        if (!Number.isFinite(uploadId)) return;
        const filename = String(name ?? "");

        const existing = state.uploadTransfers.get(uploadId);
        if (existing) {
            existing.name = existing.name || filename;
            existing.size = Number(size) || existing.size || 0;
            existing.parentId = String(parentId ?? existing.parentId ?? "");
            existing.state = "uploading";
            existing.progress = Math.max(0, Math.min(100, Number(existing.progress) || 0));
        } else {
            state.uploadTransfers.set(uploadId, {
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

        const item = state.uploadTransfers.get(uploadId);
        if (!item) return;
        item.progress = clamped;
        renderTransferItem(uploadId);
    });

    window.runtime.EventsOn("upload_complete", (id, name) => {
        const uploadId = Number(id);
        if (!Number.isFinite(uploadId)) return;
        const item = state.uploadTransfers.get(uploadId);
        if (!item) {
            state.uploadTransfers.set(uploadId, {
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

        if (state.uploadBatch) state.uploadBatch.done += 1;
        renderTransferItem(uploadId);
        updateTransferPill();

        if (state.uploadBatch && state.uploadBatch.done + state.uploadBatch.failed >= state.uploadBatch.total) {
            state.uploadBatch = null;
            window.refreshFiles();
        }
    });

    window.runtime.EventsOn("upload_error", (id, name) => {
        const uploadId = Number(id);
        if (!Number.isFinite(uploadId)) return;
        const filename = String(name ?? "");

        const item = state.uploadTransfers.get(uploadId) || {
            id: uploadId,
            name: filename,
            size: 0,
            parentId: "",
            progress: 0,
            state: "failed",
        };
        item.state = "failed";
        item.progress = 100;
        state.uploadTransfers.set(uploadId, item);

        if (state.uploadBatch) state.uploadBatch.failed += 1;
        renderTransferItem(uploadId);
        updateTransferPill();

        if (state.uploadBatch && state.uploadBatch.done + state.uploadBatch.failed >= state.uploadBatch.total) {
            state.uploadBatch = null;
            window.refreshFiles();
        }
    });

    updateTransferPill();
}

export async function uploadWithParentID(parentID) {
    const paths = await SelectFiles();
    if (!paths || !paths.length) return;

    state.activeTransfer = "upload";
    state.uploadBatch = { total: paths.length, done: 0, failed: 0 };

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
    state.uploadTransfers = nextTransfers;
    if (state.transferUploadListEl) {
        state.transferUploadListEl.querySelectorAll('.transfer-item[data-type="upload"]').forEach((el) => el.remove());
    }
    for (let i = 0; i < paths.length; i++) renderTransferItem(i);
    updateTransferPill();

    const upload = window?.go?.main?.App?.UploadToDriveFS;
    if (typeof upload !== "function") {
        state.activeTransfer = null;
        state.uploadBatch = null;
        state.uploadTransfers = new Map();
        if (state.transferUploadListEl) {
            state.transferUploadListEl.querySelectorAll('.transfer-item[data-type="upload"]').forEach((el) => el.remove());
        }
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
        if (state.activeTransfer === "upload") state.activeTransfer = null;
    }
}
