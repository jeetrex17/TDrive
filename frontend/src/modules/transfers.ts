// Upload/Download progress handling for TDrive frontend.
//
// All transfer state surfaces through the notification bell — there is no
// separate transfer pill or sheet anymore. Active transfers feed the bell's
// hover popover via pushTransferStart/updateTransferProgress/markTransferDone.
// Completed transfers stay in the bell's "Recent" panel until cleared.

import { state, type DownloadQueueItem, type DownloadState } from '../state';
import { SelectFiles, DownloadFile, DownloadFolder } from '../../wailsjs/go/main/App';
import { notify } from './notifications';
import { loadEncryptionStatus } from './encryption';
import { openUploadOptionsModal } from './modals/upload-options';
import { openImportOptionsModal } from './modals/import-options';
import { openEncryptionSetupModal } from './modals/encryption-setup';
import { openEncryptionPasswordModal } from './modals/encryption-password';
import {
    pushTransferStart,
    updateTransferProgress,
    updateTransferName,
    markTransferDone,
} from './notif-bell';
import UploadMenu from '../ui/chrome/UploadMenu.svelte';
import { mountSvelte, type SvelteMountHandle } from '../ui/mount';

let uploadMenuHandle: SvelteMountHandle<Record<string, unknown>> | null = null;

const DOWNLOAD_TERMINAL_STATES = new Set(["done", "failed", "canceled"]);

type DownloadResultPayload = {
    status?: unknown;
    message?: unknown;
    saved_path?: unknown;
};

type FolderDownloadProgressPayload = {
    folder_id?: unknown;
    percent?: unknown;
    bytes_completed?: unknown;
    bytes_total?: unknown;
    files_completed?: unknown;
    files_total?: unknown;
};

function isDownloadTerminalState(status: unknown) {
    return DOWNLOAD_TERMINAL_STATES.has(String(status || ""));
}

function asObjectRecord(value: unknown): Record<string, unknown> {
    return value && typeof value === "object" ? value as Record<string, unknown> : {};
}

function normalizeDownloadResult(result: unknown) {
    if (!result || typeof result !== "object") {
        return { status: "error", message: "Download failed", saved_path: "" };
    }

    const payload = result as DownloadResultPayload;
    const status = String(payload.status || "error").toLowerCase();
    return {
        status: status === "success" || status === "canceled" || status === "error" ? status : "error",
        message: String(payload.message || "Download failed"),
        saved_path: String(payload.saved_path || ""),
    };
}

export function setupDownloadProgress() {
    if (!window.runtime?.EventsOn) return;

    window.runtime.EventsOn("download_progress", (percent: unknown) => {
        const activeKey = state.activeDownloadId;
        if (activeKey === null) return;
        const value = Number(percent);
        if (!Number.isFinite(value)) return;

        const clamped = Math.max(0, Math.min(100, value));
        const item = state.downloadQueue.find((entry) => entry.key === activeKey);
        if (!item || item.kind !== 'file') return;
        const nextProgress = Math.max(item.progress, clamped);
        replaceDownloadItem(activeKey, (current) => ({
            ...current,
            progress: nextProgress,
            state: isDownloadTerminalState(current.state) ? current.state : 'downloading',
        }));
        updateTransferProgress({ id: item.key, direction: 'down', progress: nextProgress });
    });

    window.runtime.EventsOn("folder_download_progress", (rawPayload: unknown) => {
        const activeKey = state.activeDownloadId;
        if (activeKey === null) return;
        const item = state.downloadQueue.find((entry) => entry.key === activeKey);
        const payload = asObjectRecord(rawPayload) as FolderDownloadProgressPayload;
        if (!item || item.kind !== 'folder' || String(payload?.folder_id ?? '') !== item.id) return;

        const progress = clampFinite(payload?.percent, item.progress, 0, 100);
        const bytesCompleted = clampFinite(payload?.bytes_completed, item.bytesCompleted, 0, Number.MAX_SAFE_INTEGER);
        const bytesTotal = clampFinite(payload?.bytes_total, item.bytesTotal, 0, Number.MAX_SAFE_INTEGER);
        const filesCompleted = clampFinite(payload?.files_completed, item.filesCompleted, 0, Number.MAX_SAFE_INTEGER);
        const filesTotal = clampFinite(payload?.files_total, item.filesTotal, 0, Number.MAX_SAFE_INTEGER);
        const next = {
            progress: Math.max(item.progress, progress),
            bytesCompleted: Math.max(item.bytesCompleted, bytesCompleted),
            bytesTotal: Math.max(item.bytesTotal, bytesTotal),
            filesCompleted: Math.max(item.filesCompleted, filesCompleted),
            filesTotal: Math.max(item.filesTotal, filesTotal),
        };
        replaceDownloadItem(activeKey, (current) => ({
            ...current,
            ...next,
            size: Math.max(current.size, next.bytesTotal),
            state: isDownloadTerminalState(current.state) ? current.state : 'downloading',
        }));
        updateTransferProgress({
            id: item.key,
            direction: 'down',
            progress: next.progress,
            bytes: next.bytesCompleted,
            total: next.bytesTotal,
            itemsDone: next.filesCompleted,
            itemsTotal: next.filesTotal,
        });
    });
}

async function startNextDownload() {
    if (state.activeDownloadId !== null) return;
    const next = state.downloadQueue.find((entry) => entry.state === 'queued');
    if (!next) {
        if (state.activeTransfer === 'download') state.activeTransfer = null;
        return;
    }

    state.activeTransfer = 'download';
    state.activeDownloadId = next.key;
    replaceDownloadItem(next.key, (current) => ({
        ...current,
        state: 'downloading',
        progress: Math.max(0, Math.min(100, Number(current.progress) || 0)),
    }));
    pushTransferStart({ id: next.key, direction: 'down', name: next.name, total: next.size });

    try {
        let result = normalizeDownloadResult(await dispatchDownload(next));
        // If the backend needs the encryption password, prompt once and
        // retry. This avoids a separate per-file encryption lookup before
        // download starts.
        if (result.status === "error" && /encryption password required/i.test(result.message || "")) {
            const ok = await openEncryptionPasswordModal();
            if (ok) {
                result = normalizeDownloadResult(await dispatchDownload(next));
            }
        }

        if (result.status === "success") {
            updateDownloadResult(next.key, 'done', result.message, 100);
            markTransferDone({ id: next.key, direction: 'down', status: 'done' });
        } else if (result.status === "canceled") {
            updateDownloadResult(next.key, 'canceled', result.message);
            markTransferDone({ id: next.key, direction: 'down', status: 'canceled' });
        } else {
            const canceled = state.cancelingDownload;
            const status = canceled ? 'canceled' : 'failed';
            updateDownloadResult(next.key, status, result.message);
            markTransferDone({ id: next.key, direction: 'down', status });
        }
    } catch (err) {
        console.error("Download failed:", err);
        const canceled = state.cancelingDownload;
        const status = canceled ? 'canceled' : 'failed';
        updateDownloadResult(next.key, status, 'Download failed');
        markTransferDone({ id: next.key, direction: 'down', status });
    } finally {
        state.cancelingDownload = false;
        state.activeDownloadId = null;
        void startNextDownload();
    }
}

export function enqueueDownload(id: unknown, name: unknown, size: unknown) {
    const downloadId = Number(id);
    if (!Number.isFinite(downloadId)) return;
    const label = String(name || "Download");
    enqueueDownloadItem({
        key: `file:${downloadId}`,
        kind: 'file',
        id: downloadId,
        name: label,
        size: Number(size) || 0,
        progress: 0,
        state: 'queued',
        message: '',
        bytesCompleted: 0,
        bytesTotal: Number(size) || 0,
        filesCompleted: 0,
        filesTotal: 1,
    });
}

export function enqueueFolderDownload(id: unknown, name: unknown, size: unknown = 0) {
    const folderId = String(id ?? '').trim();
    if (!folderId) return;
    const storedSize = Math.max(0, Number(size) || 0);
    enqueueDownloadItem({
        key: `folder:${folderId}`,
        kind: 'folder',
        id: folderId,
        name: String(name || 'Folder'),
        size: storedSize,
        progress: 0,
        state: 'queued',
        message: '',
        bytesCompleted: 0,
        bytesTotal: storedSize,
        filesCompleted: 0,
        filesTotal: 0,
    });
}

function enqueueDownloadItem(item: DownloadQueueItem) {
    const existing = state.downloadQueue.find((entry) => entry.key === item.key);
    state.downloadQueue = existing
        ? state.downloadQueue.map((entry) => entry.key === item.key
            ? { ...item, name: entry.name || item.name }
            : entry)
        : [...state.downloadQueue, item];
    if (state.activeDownloadId === null) void startNextDownload();
}

function replaceDownloadItem(key: string, update: (item: DownloadQueueItem) => DownloadQueueItem) {
    state.downloadQueue = state.downloadQueue.map((item) => item.key === key ? update(item) : item);
}

function updateDownloadResult(key: string, status: DownloadState, message: string, progress?: number) {
    replaceDownloadItem(key, (item) => ({
        ...item,
        state: status,
        message,
        progress: progress ?? item.progress,
    }));
}

function dispatchDownload(item: DownloadQueueItem) {
    return item.kind === 'folder'
        ? DownloadFolder(item.id)
        : DownloadFile(item.id, item.id);
}

function clampFinite(raw: unknown, fallback: number, minValue: number, maxValue: number): number {
    const value = Number(raw);
    if (!Number.isFinite(value)) return fallback;
    return Math.max(minValue, Math.min(maxValue, value));
}

// IMPORT_TRANSFER_ID keys the single aggregate bell row for a folder/archive
// import. It is a string so it never collides with numeric per-file upload ids.
const IMPORT_TRANSFER_ID = 'import';

// flowBusy serializes import/upload flows: a second trigger (rapid menu clicks,
// or a drop during an active import) is rejected rather than corrupting the
// shared batch/transfer state.
let flowBusy = false;
const importProgressById = new Map<number, number>();
// Per-file upload ids seen during the current import, so stray per-file rows
// can be swept if a completion event is dropped.
const importFileIds = new Set<number>();
// First few distinct per-file failure reasons, folded into the aggregate
// import summary toast. Imports report one toast for the whole batch, so this
// is the only place the backend's actual error text survives to the user.
const MAX_IMPORT_FAILURE_REASONS = 3;
const importFailureReasons: string[] = [];

function recordImportFailureReason(name: any, message: any) {
    if (importFailureReasons.length >= MAX_IMPORT_FAILURE_REASONS) return;
    const reason = String(message ?? '').trim();
    if (!reason) return;
    const fname = String(name ?? '').trim();
    const entry = fname ? `${fname}: ${reason}` : reason;
    if (!importFailureReasons.includes(entry)) importFailureReasons.push(entry);
}

// refreshImportRow advances the aggregate import row as files finish.
function refreshImportRow() {
    const batch = state.importBatch;
    if (!batch) return;
    let partial = 0;
    for (const value of importProgressById.values()) partial += value;
    const finished = batch.done + batch.failed + partial;
    const total = batch.total || 0;
    const pct = total > 0 ? Math.min(100, (finished / total) * 100) : 100;
    updateTransferProgress({ id: IMPORT_TRANSFER_ID, direction: 'up', progress: pct });
    if (total > 0) {
        updateTransferName({
            id: IMPORT_TRANSFER_ID,
            direction: 'up',
            name: `Importing ${batch.done + batch.failed} / ${total}`,
        });
    }
}

// sweepImportFileRows finalizes any import per-file rows still marked active
// (e.g. if a completion event was dropped). markTransferDone is idempotent, so
// rows that already ended (done/failed) are left untouched.
function sweepImportFileRows() {
    for (const fid of importFileIds) {
        markTransferDone({ id: fid, direction: 'up', status: 'done' });
    }
    importFileIds.clear();
}

export function setupUploadProgress() {
    if (!window.runtime?.EventsOn) return;

    window.runtime.EventsOn("upload_start", (id: any, name: any, size: any, parentId: any) => {
        // During an import, also show a per-file row; the aggregate row owns the
        // batch counters and the overall progress.
        if (state.importBatch) {
            const fid = Number(id);
            if (Number.isFinite(fid)) {
                importFileIds.add(fid);
                pushTransferStart({ id: fid, direction: 'up', name: String(name ?? ""), total: Number(size) || 0 });
            }
            return;
        }

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

    window.runtime.EventsOn("upload_progress", (id: any, percent: any) => {
        const uploadId = Number(id);
        if (!Number.isFinite(uploadId)) return;
        const value = Number(percent);
        if (!Number.isFinite(value)) return;
        const clamped = Math.max(0, Math.min(100, value));

        if (state.importBatch) {
            importProgressById.set(uploadId, clamped / 100);
            updateTransferProgress({ id: uploadId, direction: 'up', progress: clamped });
            refreshImportRow();
            return;
        }

        const item = state.uploadTransfers.get(uploadId);
        if (!item) return;
        item.progress = clamped;
        updateTransferProgress({ id: uploadId, direction: 'up', progress: clamped });
    });

    window.runtime.EventsOn("upload_complete", (id: any, name: any) => {
        const uploadId = Number(id);
        if (!Number.isFinite(uploadId)) return;
        if (state.importBatch) {
            importProgressById.delete(uploadId);
            importFileIds.delete(uploadId);
            state.importBatch.done += 1;
            markTransferDone({ id: uploadId, direction: 'up', status: 'done' });
            refreshImportRow();
            return;
        }
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

    window.runtime.EventsOn("upload_error", (id: any, name: any, message: any) => {
        const uploadId = Number(id);
        if (!Number.isFinite(uploadId)) return;
        if (state.importBatch) {
            importProgressById.delete(uploadId);
            importFileIds.delete(uploadId);
            state.importBatch.failed += 1;
            if (!state.cancelingUpload) recordImportFailureReason(name, message);
            pushTransferStart({ id: uploadId, direction: 'up', name: String(name ?? "") || 'Upload failed', total: 0 });
            markTransferDone({ id: uploadId, direction: 'up', status: state.cancelingUpload ? 'canceled' : 'failed' });
            refreshImportRow();
            return;
        }
        const filename = String(name ?? "");

        const hadItem = state.uploadTransfers.has(uploadId);
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

        if (!hadItem) {
            pushTransferStart({ id: uploadId, direction: 'up', name: filename || 'Upload failed', total: 0 });
        }
        markTransferDone({ id: uploadId, direction: 'up', status: state.cancelingUpload ? 'canceled' : 'failed' });

        // Surface the backend's actual failure reason. The bell row only shows
        // a generic "failed" state, which leaves the user with nothing to act on.
        const errorBody = String(message ?? "").trim();
        if (errorBody && !state.cancelingUpload) {
            notify({
                level: 'error',
                title: filename ? `Couldn't upload "${filename}"` : 'Upload failed',
                body: errorBody,
            });
        }

        if (batchFinished) {
            window.refreshFiles();
        }
    });

    window.runtime.EventsOn("import_start", () => {
        importProgressById.clear();
        importFileIds.clear();
        importFailureReasons.length = 0;
        state.importBatch = { total: 0, done: 0, failed: 0 };
        pushTransferStart({ id: IMPORT_TRANSFER_ID, direction: 'up', name: 'Preparing import…', total: 0 });
    });

    // Live phase label: "Extracting backup.zip", "Adding Photos", etc.
    window.runtime.EventsOn("import_progress", (info: any) => {
        if (!state.importBatch) return;
        const label = String(info?.label ?? "").trim();
        if (label) updateTransferName({ id: IMPORT_TRANSFER_ID, direction: 'up', name: label });
    });

    // Folders done, uploads begin: now we know the real file count.
    window.runtime.EventsOn("import_uploading", (info: any) => {
        if (!state.importBatch) return;
        const files = Number(info?.files) || 0;
        state.importBatch.total = files;
        importProgressById.clear();
        updateTransferName({
            id: IMPORT_TRANSFER_ID,
            direction: 'up',
            name: files > 0 ? `Importing 0 / ${files}` : 'Finishing import…',
        });
        refreshImportRow();
    });

    window.runtime.EventsOn("import_complete", (info: any) => {
        const failedUploads = Math.max(Number(info?.failed) || 0, state.importBatch?.failed || 0);
        const uploaded = Number(info?.uploaded) || 0;
        const oversize = Number(info?.oversize) || 0;
        const errorCount = Array.isArray(info?.errors) ? info.errors.length : 0;
        const canceled = state.cancelingUpload;
        const status = canceled ? 'canceled' : (failedUploads > 0 ? 'failed' : 'done');
        if (!state.importBatch) {
            pushTransferStart({ id: IMPORT_TRANSFER_ID, direction: 'up', name: 'Import completed', total: uploaded + failedUploads });
        }
        updateTransferName({
            id: IMPORT_TRANSFER_ID,
            direction: 'up',
            name: canceled ? 'Import canceled' : (uploaded === 1 ? 'Imported 1 file' : `Imported ${uploaded} files`),
        });
        markTransferDone({ id: IMPORT_TRANSFER_ID, direction: 'up', status });
        state.importBatch = null;
        importProgressById.clear();
        sweepImportFileRows();

        if (typeof window.refreshFiles === 'function') window.refreshFiles();

        if (failedUploads > 0 || oversize > 0 || errorCount > 0) {
            const bits: string[] = [];
            if (failedUploads > 0) bits.push(`${failedUploads} failed`);
            if (oversize > 0) bits.push(`${oversize} skipped (too large)`);
            if (errorCount > 0) bits.push(`${errorCount} ${errorCount === 1 ? 'item' : 'items'} had errors`);
            // Include the first few concrete reasons (per-file upload errors,
            // then backend scan errors) so the summary is actionable, not just
            // counts.
            const reasons = [...importFailureReasons];
            if (Array.isArray(info?.errors)) {
                for (const raw of info.errors) {
                    if (reasons.length >= MAX_IMPORT_FAILURE_REASONS) break;
                    const text = String(raw ?? '').trim();
                    if (text && !reasons.includes(text)) reasons.push(text);
                }
            }
            notify({
                level: failedUploads > 0 ? 'error' : 'info',
                title: `Imported ${uploaded === 1 ? '1 file' : `${uploaded} files`}`,
                body: [...bits, ...reasons].join('  ·  '),
            });
        }
        importFailureReasons.length = 0;
    });
}

// uploadWithParentID opens the file picker and routes the selection through the
// shared import flow (kept as the name the context menu and window.selectFile
// already call).
export async function uploadWithParentID(parentID: any) {
    const paths = await SelectFiles();
    await runImportFlow(parentID, paths);
}

// importFolderWithParentID opens the directory picker and imports the chosen
// folder tree into parentID.
export async function importFolderWithParentID(parentID: any) {
    const selectFolder = window?.go?.main?.App?.SelectFolder;
    if (typeof selectFolder !== "function") {
        notifyBindingsMissing("SelectFolder");
        return;
    }
    let dir = "";
    try {
        dir = String((await selectFolder()) || "");
    } catch (err) {
        console.error("SelectFolder failed:", err);
        notify({ level: 'error', title: 'Could not open the folder picker', body: String(err) });
        return;
    }
    if (!dir) return;
    await runImportFlow(parentID, [dir]);
}

// runImportFlow is the single entry point for any selection (file picker, folder
// picker, or drag-drop). A plain-files selection keeps the original per-file
// upload UX; a selection containing folders or archives goes through the import
// dialog and the aggregated import flow.
async function runImportFlow(parentID: any, paths: any) {
    if (!paths || !paths.length) return;
    if (flowBusy) {
        notify({ level: 'info', title: 'A transfer is already in progress', body: 'Wait for it to finish, then start another.' });
        return;
    }
    flowBusy = true;
    try {
        const onPersonal = state.activeChannel?.kind === 'personal';
        if (onPersonal) {
            // Refresh the snapshot so the modal's follow-up steps see truth.
            await loadEncryptionStatus();
        }

        const planFn = window?.go?.main?.App?.PlanImport;
        const importFn = window?.go?.main?.App?.ImportPaths;
        if (typeof planFn !== "function" || typeof importFn !== "function") {
            notifyBindingsMissing(typeof planFn !== "function" ? "PlanImport" : "ImportPaths");
            return;
        }

        let plan: any = null;
        try {
            plan = await planFn(paths, false, false);
        } catch (err) {
            console.error("PlanImport failed:", err);
        }
        if (!plan) {
            // Don't fall through to the plain uploader: a directory path would be
            // sent to UploadToDriveFS and fail. Surface it and stop.
            notify({ level: 'error', title: 'Could not read the selection', body: 'Please try again.' });
            return;
        }

        const complex = Number(plan.folders) > 0 || Number(plan.archives) > 0;

        if (!complex) {
            // Plain files: the original flow (per-file rows, encrypt-options modal).
            let encrypt = false;
            if (onPersonal) {
                const choice: any = await openUploadOptionsModal({ count: plan.files || paths.length });
                if (!choice) return;
                encrypt = !!choice.encrypt;
                if (encrypt && !state.encryption.passwordRemembered) {
                    const ok = state.encryption.passwordSet
                        ? await openEncryptionPasswordModal()
                        : await openEncryptionSetupModal();
                    if (!ok) return;
                }
            }
            await uploadPathsBatch(paths, parentID, encrypt);
            return;
        }

        // Folders/archives: confirm via the import dialog.
        const choice = await openImportOptionsModal({
            plan,
            personal: onPersonal,
            hasArchives: Number(plan.archives) > 0,
            replan: (encrypt: boolean, extract: boolean) => planFn(paths, encrypt, extract),
        });
        if (!choice) return;

        const { encrypt, extract } = choice;
        if (encrypt && !state.encryption.passwordRemembered) {
            const ok = state.encryption.passwordSet
                ? await openEncryptionPasswordModal()
                : await openEncryptionSetupModal();
            if (!ok) return;
        }

        state.activeTransfer = "upload";
        let importThrew = false;
        try {
            await importFn(paths, parentID || "", encrypt, extract);
        } catch (err) {
            importThrew = true;
            console.error("Import failed:", err);
            notify({ level: 'error', title: 'Import failed', body: String(err) });
            if (!state.importBatch) {
                pushTransferStart({ id: IMPORT_TRANSFER_ID, direction: 'up', name: 'Import failed', total: 0 });
            }
        } finally {
            if (state.activeTransfer === "upload") state.activeTransfer = null;
            // import_complete is authoritative; if it never arrived (error or a
            // dropped event), finalize the aggregate row here with the right status.
            if (state.importBatch) {
                markTransferDone({ id: IMPORT_TRANSFER_ID, direction: 'up', status: importThrew ? 'failed' : 'done' });
                state.importBatch = null;
            }
            if (importThrew) {
                markTransferDone({ id: IMPORT_TRANSFER_ID, direction: 'up', status: 'failed' });
            }
            importProgressById.clear();
            sweepImportFileRows();
        }
    } finally {
        flowBusy = false;
        state.cancelingUpload = false;
    }
}

// uploadPathsBatch runs the classic per-file upload (one bell row per file).
async function uploadPathsBatch(paths: any, parentID: any, encrypt: boolean) {
    state.activeTransfer = "upload";
    state.uploadBatch = { total: paths.length, done: 0, failed: 0 };

    const nextTransfers = new Map();
    for (let i = 0; i < paths.length; i++) {
        const p = String(paths[i] ?? "");
        const name = p ? p.split(/[/\\]/).pop() : "Untitled";
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
        notifyBindingsMissing("UploadToDriveFS");
        return;
    }

    let uploadThrew = false;
    try {
        const parentIDs = paths.map(() => parentID || "");
        await upload(paths, parentIDs, encrypt);
    } catch (err) {
        uploadThrew = true;
        console.error("Upload failed:", err);
        // On cancel the backend returns "N uploads failed"; the per-file rows
        // already show Canceled, so don't also pop a generic failure toast.
        if (!state.cancelingUpload) {
            notify({ level: 'error', title: 'Upload failed', body: String(err) });
        }
    } finally {
        if (state.activeTransfer === "upload") state.activeTransfer = null;
        // Safety sweep: by the time UploadToDriveFS resolves, every upload in
        // the batch has terminated on the backend. If a Wails event was dropped,
        // an entry may still be stuck 'active' at 100% in the bell.
        // markTransferDone is idempotent against terminal entries.
        for (const [uploadId, item] of state.uploadTransfers) {
            if (state.cancelingUpload) {
                markTransferDone({ id: uploadId, direction: 'up', status: 'canceled' });
            } else if (uploadThrew && item?.state !== 'done' && item?.state !== 'failed') {
                pushTransferStart({ id: uploadId, direction: 'up', name: item?.name || 'Upload failed', total: item?.size || 0 });
                markTransferDone({ id: uploadId, direction: 'up', status: 'failed' });
            } else {
                markTransferDone({ id: uploadId, direction: 'up', status: 'done' });
            }
        }
        state.uploadBatch = null;
        state.cancelingUpload = false;
    }
}

function notifyBindingsMissing(name: string) {
    notify({
        level: 'error',
        title: 'Upload bindings missing',
        body: `${name} is missing in the backend. Rebuild the app (wails dev/build) and try again.`,
    });
}

// setupUploadMenu wires the Upload button's popover (Files / Folder). The OS
// dialogs cannot select files and folders together, so the entry point splits
// them; drag-drop covers truly mixed selections.
export function setupUploadMenu() {
    const host = document.getElementById('upload-menu-root');
    if (!host || uploadMenuHandle) return;

    host.replaceChildren();
    uploadMenuHandle = mountSvelte(UploadMenu, {
        target: host,
        props: {
            onFiles: () => {
                void uploadWithParentID(state.currentFolderId);
            },
            onFolder: () => {
                void importFolderWithParentID(state.currentFolderId);
            },
        },
    });
}

// setupFileDrop handles native OS file drops (mixed files + folders) forwarded
// by the Go side. The drop target is the current folder.
export function setupFileDrop() {
    if (!window.runtime?.EventsOn) return;
    window.runtime.EventsOn('files_dropped', (payload: any) => {
        // If an in-app drag-to-move is underway, ignore native drops entirely
        // (macOS can still fire one for the internal drag).
        if (state.dragState) return;
        const paths = Array.isArray(payload) ? payload : payload?.paths;
        if (!Array.isArray(paths) || !paths.length) return;
        if (!state.activeChannel) return; // ignore drops before a drive is open
        const x = Number(payload?.x);
        const y = Number(payload?.y);
        if (!Number.isFinite(x) || !Number.isFinite(y)) return;
        const target = document.elementFromPoint(x, y);
        if (!target || !(target as HTMLElement).closest('#file-list')) return;
        void runImportFlow(state.currentFolderId, paths);
    });
}
