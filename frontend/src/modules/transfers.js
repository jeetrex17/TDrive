// Upload/Download progress handling for TDrive frontend

import { state } from '../state.js';
import { escapeHtml } from '../utils.js';
import { SelectFiles } from '../../wailsjs/go/main/App';

export function showDownloadProgress(percent) {
    if (!state.downloadProgressEl || !state.downloadProgressFillEl) return;

    const value = Number(percent);
    if (!Number.isFinite(value)) return;

    const clamped = Math.max(0, Math.min(100, value));

    if (state.downloadProgressHideTimeout) {
        clearTimeout(state.downloadProgressHideTimeout);
        state.downloadProgressHideTimeout = null;
    }

    state.downloadProgressEl.style.display = "block";
    state.downloadProgressEl.setAttribute("aria-valuenow", String(Math.round(clamped)));
    state.downloadProgressFillEl.style.width = `${clamped}%`;
}

export function hideDownloadProgress() {
    if (!state.downloadProgressEl || !state.downloadProgressFillEl) return;

    if (state.downloadProgressHideTimeout) {
        clearTimeout(state.downloadProgressHideTimeout);
        state.downloadProgressHideTimeout = null;
    }

    state.downloadProgressEl.style.display = "none";
    state.downloadProgressEl.setAttribute("aria-valuenow", "0");
    state.downloadProgressFillEl.style.width = "0%";
}

export function setupDownloadProgress() {
    state.downloadProgressEl = document.getElementById("transfer-progress");
    state.downloadProgressFillEl = document.getElementById("transfer-progress-fill");
    if (!state.downloadProgressEl || !state.downloadProgressFillEl) return;

    if (!window.runtime?.EventsOn) return;

    window.runtime.EventsOn("download_progress", (percent) => {
        if (state.activeTransfer !== "download") return;
        const value = Number(percent);
        if (!Number.isFinite(value)) return;

        const clamped = Math.max(0, Math.min(100, value));
        showDownloadProgress(clamped);

        const status = document.getElementById("status-msg");
        if (status) status.innerText = `Downloading… ${Math.round(clamped)}%`;

        if (clamped >= 100) {
            if (state.downloadProgressHideTimeout) clearTimeout(state.downloadProgressHideTimeout);
            state.downloadProgressHideTimeout = setTimeout(() => {
                hideDownloadProgress();
            }, 900);
        }
    });
}

export function updateTransferPill() {
    if (!state.transferPillEl) return;
    const hasTransfers = state.uploadTransfers && state.uploadTransfers.size > 0;
    const hasBatch = Boolean(state.uploadBatch);

    state.transferPillEl.style.display = "inline-flex";
    state.transferPillEl.classList.toggle("is-idle", !hasTransfers && !hasBatch);
    state.transferPillEl.classList.toggle("is-active", hasBatch);

    if (!hasTransfers && !hasBatch) {
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

    const hasFailures = failed > 0;
    state.transferPillEl.classList.toggle("is-error", hasFailures);

    const label = state.uploadBatch ? `Uploading ${done}/${total}` : hasFailures ? `Uploads finished (${failed} failed)` : "Uploads";
    state.transferPillEl.innerHTML = `<span class="transfer-pill-dot" aria-hidden="true"></span><span class="transfer-pill-label">${escapeHtml(label)}</span>`;

    const allDone = done + failed >= total && total > 0;
    if (state.transferClearEl) state.transferClearEl.style.display = allDone ? "inline-flex" : "none";
}

export function renderTransferItem(uploadId) {
    if (!state.transferUploadListEl) return;
    const item = state.uploadTransfers.get(uploadId);
    if (!item) return;

    let el = state.transferUploadListEl.querySelector(`.transfer-item[data-id="${uploadId}"]`);
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

export function setupUploadProgress() {
    if (!window.runtime?.EventsOn) return;

    state.transferPillEl = document.getElementById("transfer-pill");
    state.transferSheetEl = document.getElementById("transfer-sheet");
    state.transferUploadListEl = document.getElementById("transfer-upload-list");
    state.transferClearEl = document.getElementById("transfer-clear");

    if (state.transferPillEl) {
        state.transferPillEl.addEventListener("click", () => {
            if (!state.transferSheetEl) return;
            if ((!state.uploadTransfers || state.uploadTransfers.size === 0) && !state.uploadBatch) return;
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
    if (state.transferUploadListEl) state.transferUploadListEl.innerHTML = "";
    for (let i = 0; i < paths.length; i++) renderTransferItem(i);
    updateTransferPill();

    const upload = window?.go?.main?.App?.UploadToDriveFS;
    if (typeof upload !== "function") {
        state.activeTransfer = null;
        state.uploadBatch = null;
        state.uploadTransfers = new Map();
        if (state.transferUploadListEl) state.transferUploadListEl.innerHTML = "";
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
