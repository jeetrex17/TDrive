// Upload/Download progress handling for TDrive frontend.
//
// All transfer state surfaces through the notification bell — there is no
// separate transfer pill or sheet anymore. Active transfers feed the bell's
// hover popover via pushTransferStart/updateTransferProgress/markTransferDone.
// Completed transfers stay in the bell's "Recent" panel until cleared.

import { state } from '../state.js';
import { SelectFiles, DownloadFile } from '../../wailsjs/go/main/App';
import { notify } from './notifications.js';
import {
    pushTransferStart,
    updateTransferProgress,
    markTransferDone,
} from './notif-bell.js';

const DOWNLOAD_TERMINAL_STATES = new Set(["done", "failed", "canceled"]);

function isDownloadTerminalState(status) {
    return DOWNLOAD_TERMINAL_STATES.has(String(status || ""));
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

export function showDownloadProgress(percent) {
    const value = Number(percent);
    if (!Number.isFinite(value)) return;
    const activeId = state.activeDownloadId;
    if (activeId === null || activeId === undefined) return;

    const item = (state.downloadQueue || []).find((entry) => entry.id === activeId);
    if (!item) return;

    const clamped = Math.max(0, Math.min(100, value));
    item.progress = clamped;
    if (!isDownloadTerminalState(item.state)) {
        item.state = "downloading";
    }
    updateTransferProgress({ id: item.id, direction: 'down', progress: clamped });

    if (state.downloadProgressHideTimeout) {
        clearTimeout(state.downloadProgressHideTimeout);
        state.downloadProgressHideTimeout = null;
    }

    if (state.downloadProgressEl && state.downloadProgressFillEl) {
        // The thin overlay bar at the top of the file list stays as a
        // secondary signal during active downloads. Bell handles the
        // primary surface.
        state.downloadProgressEl.style.display = "block";
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
    pushTransferStart({ id: next.id, direction: 'down', name: next.name, total: next.size });

    try {
        const result = normalizeDownloadResult(await DownloadFile(Number(next.id), Number(next.id)));
        next.message = result.message;

        if (result.status === "success") {
            next.state = "done";
            next.progress = 100;
            markTransferDone({ id: next.id, direction: 'down', status: 'done' });
        } else if (result.status === "canceled") {
            next.state = "canceled";
            markTransferDone({ id: next.id, direction: 'down', status: 'canceled' });
        } else {
            next.state = "failed";
            markTransferDone({ id: next.id, direction: 'down', status: 'failed' });
        }
    } catch (err) {
        console.error("Download failed:", err);
        next.message = "Download failed";
        next.state = "failed";
        markTransferDone({ id: next.id, direction: 'down', status: 'failed' });
    } finally {
        state.activeDownloadId = null;
        hideDownloadProgress();
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
    if (state.activeDownloadId === null || state.activeDownloadId === undefined) startNextDownload();
}

export function setupUploadProgress() {
    if (!window.runtime?.EventsOn) return;

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

        pushTransferStart({ id: uploadId, direction: 'up', name: filename, total: Number(size) || 0 });
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
        updateTransferProgress({ id: uploadId, direction: 'up', progress: clamped });
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
        const batchFinished = Boolean(state.uploadBatch && state.uploadBatch.done + state.uploadBatch.failed >= state.uploadBatch.total);
        if (batchFinished) state.uploadBatch = null;

        markTransferDone({ id: uploadId, direction: 'up', status: 'done' });

        if (batchFinished) {
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
        const batchFinished = Boolean(state.uploadBatch && state.uploadBatch.done + state.uploadBatch.failed >= state.uploadBatch.total);
        if (batchFinished) state.uploadBatch = null;

        markTransferDone({ id: uploadId, direction: 'up', status: 'failed' });

        if (batchFinished) {
            window.refreshFiles();
        }
    });
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

    const upload = window?.go?.main?.App?.UploadToDriveFS;
    if (typeof upload !== "function") {
        state.activeTransfer = null;
        state.uploadBatch = null;
        state.uploadTransfers = new Map();
        notify({
            level: 'error',
            title: 'Upload bindings missing',
            body: 'UploadToDriveFS is missing in the backend. Rebuild the app (wails dev/build) and try again.',
        });
        return;
    }

    try {
        const parentIDs = paths.map(() => parentID || "");
        await upload(paths, parentIDs);
    } catch (err) {
        console.error("Upload failed:", err);
    } finally {
        if (state.activeTransfer === "upload") state.activeTransfer = null;
        // Safety sweep: by the time UploadToDriveFS resolves, every upload
        // in the batch has terminated on the backend. If a Wails event was
        // dropped, an entry may still be stuck in 'active' at 100% in the
        // bell. markTransferDone is idempotent against terminal entries,
        // so this only flips orphaned 'active' rows.
        for (const [uploadId] of state.uploadTransfers) {
            markTransferDone({ id: uploadId, direction: 'up', status: 'done' });
        }
        state.uploadBatch = null;
    }
}
