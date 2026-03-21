// Upload/Download progress handling for TDrive frontend

import { state } from '../state.js';
import { escapeHtml } from '../utils.js';
import { SelectFiles, DownloadFile, CancelDownload, CancelUpload } from '../../wailsjs/go/main/App';

const DOWNLOAD_TERMINAL_STATES = new Set(["done", "failed", "canceled"]);
const UPLOAD_TERMINAL_STATES = new Set(["done", "failed", "canceled"]);

function isDownloadTerminalState(status) {
    return DOWNLOAD_TERMINAL_STATES.has(String(status || ""));
}

function isUploadTerminalState(status) {
    return UPLOAD_TERMINAL_STATES.has(String(status || ""));
}

function normalizeDownloadResult(result) {
    if (!result || typeof result !== "object") {
        return { status: "error", message: "Download failed", saved_path: "" };
    }

    const status = String(result.status || "error").toLowerCase();
    return {
        status: status === "success" || status === "canceled" || status === "error" ? status : "error",
        message: String(result.message || "Download failed"),
        saved_path: String(result.saved_path || ""),
    };
}

function getDownloadSummaryLabel(counts) {
    if (counts.failed > 0 && counts.canceled > 0) {
        return `Downloads stopped (${counts.failed} failed, ${counts.canceled} canceled)`;
    }
    if (counts.failed > 0) {
        return `Downloads finished (${counts.failed} failed)`;
    }
    if (counts.canceled > 0) {
        return `Downloads canceled (${counts.canceled})`;
    }
    return "Downloads finished";
}

function getUploadSummaryLabel(counts) {
    if (counts.failed > 0 && counts.canceled > 0) {
        return `Uploads stopped (${counts.failed} failed, ${counts.canceled} canceled)`;
    }
    if (counts.failed > 0) {
        return `Uploads finished (${counts.failed} failed)`;
    }
    if (counts.canceled > 0) {
        return `Uploads canceled (${counts.canceled})`;
    }
    return "Uploads finished";
}

export function showDownloadProgress(percent) {
    const value = Number(percent);
    if (!Number.isFinite(value)) return false;
    const activeId = state.activeDownloadId;
    if (activeId === null || activeId === undefined) return false;

    const item = (state.downloadQueue || []).find((entry) => entry.id === activeId);
    if (!item) return false;
    if (item.state === "canceling" || isDownloadTerminalState(item.state)) return false;

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

    return true;
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
        const updated = showDownloadProgress(clamped);

        const status = document.getElementById("status-msg");
        if (updated && status) status.innerText = `Downloading… ${Math.round(clamped)}%`;
    });
}

function getDownloadCounts() {
    const items = Array.isArray(state.downloadQueue) ? state.downloadQueue : [];
    let total = items.length;
    let done = 0;
    let failed = 0;
    let canceled = 0;
    let queued = 0;
    let downloading = 0;
    let canceling = 0;

    items.forEach((item) => {
        const status = String(item?.state || "queued");
        if (status === "done") done += 1;
        else if (status === "failed") failed += 1;
        else if (status === "canceled") canceled += 1;
        else if (status === "downloading") downloading += 1;
        else if (status === "canceling") canceling += 1;
        else queued += 1;
    });

    return { total, done, failed, canceled, queued, downloading, canceling };
}

function getUploadCounts() {
    const items = state.uploadTransfers instanceof Map ? Array.from(state.uploadTransfers.values()) : [];
    let total = items.length;
    let done = 0;
    let failed = 0;
    let canceled = 0;
    let queued = 0;
    let uploading = 0;
    let canceling = 0;

    items.forEach((item) => {
        const status = String(item?.state || "queued");
        if (status === "done") done += 1;
        else if (status === "failed") failed += 1;
        else if (status === "canceled") canceled += 1;
        else if (status === "uploading") uploading += 1;
        else if (status === "canceling") canceling += 1;
        else queued += 1;
    });

    return { total, done, failed, canceled, queued, uploading, canceling };
}

function reconcileUploadBatch() {
    const counts = getUploadCounts();
    const settled = counts.done + counts.failed + counts.canceled;
    const hasInFlightUploads = counts.uploading > 0 || counts.canceling > 0 || counts.queued > 0;

    if (state.uploadBatch && !hasInFlightUploads && counts.total > 0 && settled >= counts.total) {
        state.uploadBatch = null;
    }

    return counts;
}

export function updateTransferPill() {
    if (!state.transferPillEl) return;
    const uploadCounts = reconcileUploadBatch();
    const hasUploads = uploadCounts.total > 0;
    const hasBatch = Boolean(state.uploadBatch);
    const hasActiveUploads = uploadCounts.uploading > 0 || uploadCounts.canceling > 0 || uploadCounts.queued > 0;
    const downloadCounts = getDownloadCounts();
    const hasDownloads = downloadCounts.total > 0;

    const hasAnyTransfers = hasUploads || hasBatch || hasDownloads;

    if (!hasAnyTransfers) {
        state.transferPillEl.style.display = "none";
        return;
    }

    state.transferPillEl.style.display = "inline-flex";
    state.transferPillEl.classList.toggle("is-active", hasActiveUploads || downloadCounts.downloading > 0 || downloadCounts.canceling > 0);

    const uploadFailed = uploadCounts.failed;
    const downloadFailed = downloadCounts.failed;
    const hasFailures = uploadFailed > 0 || downloadFailed > 0;
    state.transferPillEl.classList.toggle("is-error", hasFailures);

    let label = "";
    const uploadLabel = hasUploads
        ? (
            uploadCounts.canceling > 0 && uploadCounts.uploading === 0 && uploadCounts.queued === 0
                ? "Canceling uploads..."
                : hasActiveUploads
                    ? `Uploading ${uploadCounts.done + uploadCounts.failed + uploadCounts.canceled}/${uploadCounts.total}`
                    : getUploadSummaryLabel(uploadCounts)
        )
        : "";

    let downloadLabel = "";
    if (hasDownloads) {
        if (downloadCounts.canceling > 0 && downloadCounts.downloading === 0) {
            downloadLabel = "Canceling download...";
        } else if (downloadCounts.downloading > 0) {
            const current = Math.min(downloadCounts.done + downloadCounts.failed + downloadCounts.canceled + 1, downloadCounts.total);
            downloadLabel = `Downloading ${current}/${downloadCounts.total}`;
        } else if (downloadCounts.queued > 0) {
            downloadLabel = `Downloads queued (${downloadCounts.queued})`;
        } else {
            downloadLabel = getDownloadSummaryLabel(downloadCounts);
        }
    }

    if (uploadLabel && downloadLabel) label = `${uploadLabel} · ${downloadLabel}`;
    else label = uploadLabel || downloadLabel || "";

    state.transferPillEl.innerHTML = `<span class="transfer-pill-dot" aria-hidden="true"></span><span class="transfer-pill-label">${escapeHtml(label)}</span>`;

    const uploadsDone = !hasUploads || !hasActiveUploads;
    const downloadsDone = !hasDownloads || (downloadCounts.downloading === 0 && downloadCounts.canceling === 0 && downloadCounts.queued === 0);
    const allDone = uploadsDone && downloadsDone;
    if (state.transferClearEl) state.transferClearEl.style.display = allDone ? "inline-flex" : "none";
}

function setTransferAction(el, { label = "", hidden = true, disabled = false, onClick = null, ariaLabel = "" } = {}) {
    if (!el) return;
    const actionBtn = el.querySelector(".transfer-item-action");
    if (!actionBtn) return;

    actionBtn.hidden = hidden;
    actionBtn.disabled = disabled;
    actionBtn.textContent = label;
    actionBtn.setAttribute("aria-label", ariaLabel || label || "Transfer action");
    actionBtn.onclick = onClick;
}

async function cancelUploadTransfer(uploadId) {
    const item = state.uploadTransfers.get(uploadId);
    if (!item) return;
    if (isUploadTerminalState(item.state) || item.state === "canceling") return;

    const isQueued = item.state === "queued";
    state.pendingUploadCancelIds.add(uploadId);
    item.state = "canceled";
    item.message = "Upload canceled";
    renderTransferItem(uploadId);
    reconcileUploadBatch();
    updateTransferPill();

    try {
        if (!isQueued) {
            await CancelUpload(uploadId);
        }
    } catch (err) {
        console.error("Failed to cancel upload:", err);
    }
}

async function cancelDownloadTransfer(downloadId) {
    const queue = Array.isArray(state.downloadQueue) ? state.downloadQueue : [];
    const item = queue.find((entry) => entry.id === downloadId);
    if (!item) return;
    if (isDownloadTerminalState(item.state) || item.state === "canceling") return;

    if (item.state === "queued") {
        item.state = "canceled";
        item.message = "Download canceled";
        renderDownloadItem(item);
        updateTransferPill();
        return;
    }

    state.pendingDownloadCancelIds.add(downloadId);
    item.state = "canceling";
    item.message = "Canceling...";
    renderDownloadItem(item);
    updateTransferPill();

    try {
        await CancelDownload(downloadId);
    } catch (err) {
        console.error("Failed to cancel download:", err);
    }
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
                <div class="transfer-item-main">
                    <div class="transfer-item-name"></div>
                    <div class="transfer-item-meta"></div>
                </div>
                <button class="transfer-item-action" type="button" hidden></button>
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
    if (item.state === "canceling") meta = "Canceling...";
    if (item.state === "done") meta = "Done";
    if (item.state === "failed") meta = "Failed";
    if (item.state === "canceled") meta = item.message || "Canceled";
    if ((item.state === "failed" || item.state === "canceled") && item.message) meta = item.message;
    if (metaEl) metaEl.textContent = meta;

    el.classList.toggle("is-done", item.state === "done");
    el.classList.toggle("is-failed", item.state === "failed");
    el.classList.toggle("is-queued", item.state === "queued");
    el.classList.toggle("is-canceled", item.state === "canceled");

    setTransferAction(el, {
        label: "Cancel",
        hidden: isUploadTerminalState(item.state),
        disabled: item.state === "canceling",
        ariaLabel: `Cancel upload ${item.name || "upload"}`,
        onClick: () => {
            void cancelUploadTransfer(uploadId);
        },
    });
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
                <div class="transfer-item-main">
                    <div class="transfer-item-name"></div>
                    <div class="transfer-item-meta"></div>
                </div>
                <button class="transfer-item-action" type="button" hidden></button>
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
    if (item.state === "canceling") meta = "Canceling...";
    if (item.state === "done") meta = "Done";
    if (item.state === "failed") meta = "Failed";
    if (item.state === "canceled") meta = "Canceled";
    if ((item.state === "failed" || item.state === "canceled") && item.message) meta = item.message;
    if (metaEl) metaEl.textContent = meta;

    el.classList.toggle("is-done", item.state === "done");
    el.classList.toggle("is-failed", item.state === "failed");
    el.classList.toggle("is-queued", item.state === "queued");
    el.classList.toggle("is-canceled", item.state === "canceled");

    setTransferAction(el, {
        label: "Cancel",
        hidden: isDownloadTerminalState(item.state),
        disabled: item.state === "canceling",
        ariaLabel: `Cancel download ${item.name || "download"}`,
        onClick: () => {
            void cancelDownloadTransfer(id);
        },
    });
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
        const result = normalizeDownloadResult(await DownloadFile(Number(next.id), Number(next.id)));
        const canceledByUser = state.pendingDownloadCancelIds.has(next.id);
        next.message = result.message;

        if (result.status === "success") {
            next.state = "done";
            next.progress = 100;
        } else if (result.status === "canceled" || canceledByUser) {
            next.state = "canceled";
            next.message = result.message || "Download canceled";
        } else {
            next.state = "failed";
        }
    } catch (err) {
        console.error("Download failed:", err);
        if (state.pendingDownloadCancelIds.has(next.id)) {
            next.message = "Download canceled";
            next.state = "canceled";
        } else {
            next.message = "Download failed";
            next.state = "failed";
        }
    } finally {
        state.pendingDownloadCancelIds.delete(next.id);
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
        existing.message = "";
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
        message: "",
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
            state.pendingUploadCancelIds = new Set();
            state.pendingDownloadCancelIds = new Set();
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
        const shouldCancel = state.pendingUploadCancelIds.has(uploadId);

        const existing = state.uploadTransfers.get(uploadId);
        if (existing) {
            existing.name = existing.name || filename;
            existing.size = Number(size) || existing.size || 0;
            existing.parentId = String(parentId ?? existing.parentId ?? "");
            existing.state = shouldCancel ? "canceled" : "uploading";
            existing.progress = Math.max(0, Math.min(100, Number(existing.progress) || 0));
        } else {
            state.uploadTransfers.set(uploadId, {
                id: uploadId,
                name: filename,
                size: Number(size) || 0,
                parentId: String(parentId ?? ""),
                progress: 0,
                state: shouldCancel ? "canceled" : "uploading",
                message: shouldCancel ? "Upload canceled" : "",
            });
        }

        renderTransferItem(uploadId);
        reconcileUploadBatch();
        updateTransferPill();

        if (shouldCancel) {
            void CancelUpload(uploadId).catch((err) => {
                console.error("Failed to cancel upload after start:", err);
            });
        }
    });

    window.runtime.EventsOn("upload_progress", (id, percent) => {
        const uploadId = Number(id);
        if (!Number.isFinite(uploadId)) return;
        const value = Number(percent);
        if (!Number.isFinite(value)) return;
        const clamped = Math.max(0, Math.min(100, value));

        const item = state.uploadTransfers.get(uploadId);
        if (!item) return;
        if (state.pendingUploadCancelIds.has(uploadId) || item.state === "canceling" || isUploadTerminalState(item.state)) return;
        item.progress = clamped;
        renderTransferItem(uploadId);
    });

    window.runtime.EventsOn("upload_complete", (id, name) => {
        const uploadId = Number(id);
        if (!Number.isFinite(uploadId)) return;
        const item = state.uploadTransfers.get(uploadId);
        const canceled = state.pendingUploadCancelIds.has(uploadId) || item?.state === "canceled";
        state.pendingUploadCancelIds.delete(uploadId);

        if (canceled) {
            if (item) {
                item.name = item.name || String(name ?? "");
                item.progress = Math.max(0, Math.min(100, Number(item.progress) || 0));
                item.state = "canceled";
                item.message = "Upload canceled";
                renderTransferItem(uploadId);
            }
            reconcileUploadBatch();
            updateTransferPill();
            return;
        }

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

        renderTransferItem(uploadId);
        const counts = reconcileUploadBatch();
        const batchFinished = Boolean(!state.uploadBatch && counts.total > 0);
        updateTransferPill();

        if (batchFinished) {
            window.refreshFiles();
        }
    });

    window.runtime.EventsOn("upload_error", (id, name, message) => {
        const uploadId = Number(id);
        if (!Number.isFinite(uploadId)) return;
        const filename = String(name ?? "");
        state.pendingUploadCancelIds.delete(uploadId);

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
        item.message = String(message || "Upload failed");
        state.uploadTransfers.set(uploadId, item);

        renderTransferItem(uploadId);
        const counts = reconcileUploadBatch();
        const batchFinished = Boolean(!state.uploadBatch && counts.total > 0);
        updateTransferPill();

        if (batchFinished) {
            window.refreshFiles();
        }
    });

    window.runtime.EventsOn("upload_canceled", (id, name) => {
        const uploadId = Number(id);
        if (!Number.isFinite(uploadId)) return;
        state.pendingUploadCancelIds.delete(uploadId);

        const item = state.uploadTransfers.get(uploadId) || {
            id: uploadId,
            name: String(name ?? ""),
            size: 0,
            parentId: "",
            progress: 0,
            state: "canceled",
        };
        item.name = item.name || String(name ?? "");
        item.state = "canceled";
        item.message = "Upload canceled";
        state.uploadTransfers.set(uploadId, item);

        renderTransferItem(uploadId);
        reconcileUploadBatch();
        updateTransferPill();
    });

    updateTransferPill();
}

export async function uploadWithParentID(parentID) {
    const paths = await SelectFiles();
    if (!paths || !paths.length) return;

    state.activeTransfer = "upload";
    state.uploadBatch = { total: paths.length, done: 0, failed: 0, canceled: 0 };
    state.pendingUploadCancelIds = new Set();

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
        state.pendingUploadCancelIds = new Set();
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
        reconcileUploadBatch();
        updateTransferPill();
    }
}
