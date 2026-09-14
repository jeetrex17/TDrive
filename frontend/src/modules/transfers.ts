// Upload/Download progress handling for TDrive frontend.
//
// All transfer state surfaces through the notification bell — there is no
// separate transfer pill or sheet anymore. Active transfers feed the bell's
// hover popover via pushTransferStart/updateTransferProgress/markTransferDone.
// Completed transfers stay in the bell's "Recent" panel until cleared.

import { invalidateFolderIndex, state, setTransferDirectionActive, type DownloadQueueItem } from '../state';
import { downloadFile, downloadFolder, importPaths, onNativeFileDrop, onRuntimeEvent, planImport, selectFiles, selectFolder, uploadToDriveFs } from '../api';
import type { ImportPlan, OperationError } from '../types';
import { notify } from './notifications';
import { humanizeBackendError } from './errors';
import { appActions } from './app-actions';
import { loadEncryptionStatus } from './encryption';
import { openUploadOptionsModal } from './modals/upload-options';
import { openImportOptionsModal } from './modals/import-options';
import { openEncryptionSetupModal } from './modals/encryption-setup';
import { openEncryptionPasswordModal } from './modals/encryption-password';
import { createImportProgress, reduceImportProgress } from './import-progress';
import {
    pushTransferStart,
    updateTransferProgress,
    updateTransferName,
    markTransferDone,
} from './notif-bell';
import UploadMenu from '../ui/chrome/UploadMenu.svelte';
import { mountSvelte, type SvelteMountHandle } from '../ui/mount';


function subscribeTransferEvent<TArgs extends unknown[]>(eventName: string, callback: (...data: TArgs) => void): void {
    onRuntimeEvent<TArgs>(eventName, callback);
}

let uploadMenuHandle: SvelteMountHandle<Record<string, unknown>> | null = null;




let activeTransferDriveId: number | null = null;

function invalidateTransferCaches(): void {
    invalidateFolderIndex(activeTransferDriveId);
    const driveKey = activeTransferDriveId === null ? null : String(activeTransferDriveId);
    if (driveKey && state.telegramRootCacheDriveKey === driveKey) {
        state.telegramRootCache = null;
        state.telegramRootCacheDriveKey = null;
    }
}
type FolderDownloadProgressPayload = {
    folder_id?: unknown;
    percent?: unknown;
    bytes_completed?: unknown;
    bytes_total?: unknown;
    files_completed?: unknown;
    files_total?: unknown;
};

function asObjectRecord(value: unknown): Record<string, unknown> {
    return value && typeof value === "object" ? value as Record<string, unknown> : {};
}


export function setupDownloadProgress() {
    subscribeTransferEvent<[unknown]>("download_progress", (percent) => {
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
            state: 'downloading',
        }));
        updateTransferProgress({ id: item.key, direction: 'down', progress: nextProgress });
    });

    subscribeTransferEvent<[unknown]>("folder_download_progress", (rawPayload) => {
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
            state: 'downloading',
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
        setTransferDirectionActive('download', false);
        return;
    }

    setTransferDirectionActive('download', true);
    state.activeDownloadId = next.key;
    replaceDownloadItem(next.key, (current) => ({
        ...current,
        state: 'downloading',
        progress: Math.max(0, Math.min(100, Number(current.progress) || 0)),
    }));
    pushTransferStart({ id: next.key, direction: 'down', name: next.name, total: next.size });

    try {
        let result = await dispatchDownload(next);
        if (!result.result.ok && result.result.error.code === 'encryption_password_required') {
            const unlocked = await openEncryptionPasswordModal();
            if (!unlocked) {
                finalizeDownload(next.key, 'canceled');
                return;
            }
            result = await dispatchDownload(next);
        }

        if (result.result.ok) {
            finalizeDownload(next.key, 'done');
            if (next.kind === 'folder') {
                notify({
                    level: 'success',
                    title: 'Folder downloaded',
                    body: result.savedPath ? 'Saved to ' + result.savedPath : next.name + ' saved',
                });
            }
        } else if (result.result.error.code === 'canceled') {
            finalizeDownload(next.key, 'canceled');
        } else {
            const canceled = state.cancelingDownload;
            finalizeDownload(next.key, canceled ? 'canceled' : 'failed');
            if (!canceled) notifyDownloadFailure(next, result.result.error);
        }
    } catch (err) {
        console.error("Download failed:", err);
        const canceled = state.cancelingDownload;
        finalizeDownload(next.key, canceled ? 'canceled' : 'failed');
        if (!canceled) notifyDownloadFailure(next, "Download failed");
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

function finalizeDownload(key: string, status: 'done' | 'failed' | 'canceled'): void {
    // The bell owns the bounded terminal history. Keep the scheduler limited
    // to queued and active jobs by removing a job only after that finalization.
    markTransferDone({ id: key, direction: 'down', status });
    state.downloadQueue = state.downloadQueue.filter((item) => item.key !== key);
}

// Surface the backend's display message because the terminal bell row carries
// only status, not the reason the download failed.
function notifyDownloadFailure(item: DownloadQueueItem, error: OperationError | string): void {
    const reason = humanizeBackendError(error);
    const noun = item.kind === 'folder' ? 'Folder' : 'File';
    if (typeof error !== 'string' && error.code === 'already_exists') {
        notify({ level: 'warning', title: noun + ' already exists', body: reason });
        return;
    }
    notify({
        level: 'error',
        title: `Couldn't download ${item.name}`,
        body: reason || 'The download could not be completed.',
    });
}

function dispatchDownload(item: DownloadQueueItem) {
    return item.kind === 'folder'
        ? downloadFolder(item.id)
        : downloadFile(item.id, item.id);
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
// First few distinct per-file failure reasons, folded into the aggregate
// import summary toast. Imports report one toast for the whole batch, so this
// is the only place the backend's actual error text survives to the user.
const MAX_IMPORT_FAILURE_REASONS = 3;
const importFailureReasons: string[] = [];
let importCompleteReceived = false;

function recordImportFailureReason(name: any, message: any) {
    if (importFailureReasons.length >= MAX_IMPORT_FAILURE_REASONS) return;
    const reason = String(message ?? '').trim();
    if (!reason) return;
    const fname = String(name ?? '').trim();
    const entry = fname ? `${fname}: ${reason}` : reason;
    if (!importFailureReasons.includes(entry)) importFailureReasons.push(entry);
}

function formatImportResultLabel(uploaded: number, failedUploads = 0) {
    const imported = uploaded === 1 ? 'Imported 1 file' : `Imported ${uploaded} files`;
    return failedUploads > 0 ? `${imported} · ${failedUploads} failed` : imported;
}

// refreshImportRow renders one constant-size aggregate supplied by the backend.
function refreshImportRow() {
    const batch = state.importBatch;
    if (!batch) return;
    const total = batch.total || 0;
    updateTransferProgress({ id: IMPORT_TRANSFER_ID, direction: 'up', progress: batch.progress });
    if (total > 0) {
        updateTransferName({
            id: IMPORT_TRANSFER_ID,
            direction: 'up',
            name: `Importing ${batch.done + batch.failed} / ${total}`,
        });
    }
}

export function setupUploadProgress() {
    subscribeTransferEvent<[unknown, unknown, unknown, unknown]>("upload_start", (id, name, size, parentId) => {
        // New backends suppress detailed events during imports. Ignore any
        // strays from an older backend so a large import still keeps one row.
        if (state.importBatch) {
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

    subscribeTransferEvent<[unknown, unknown]>("upload_progress", (id, percent) => {
        const uploadId = Number(id);
        if (!Number.isFinite(uploadId)) return;
        const value = Number(percent);
        if (!Number.isFinite(value)) return;
        const clamped = Math.max(0, Math.min(100, value));

        if (state.importBatch) {
            return;
        }

        const item = state.uploadTransfers.get(uploadId);
        if (!item) return;
        item.progress = clamped;
        updateTransferProgress({ id: uploadId, direction: 'up', progress: clamped });
    });

    subscribeTransferEvent<[unknown, unknown]>("upload_complete", (id, name) => {
        const uploadId = Number(id);
        if (!Number.isFinite(uploadId)) return;
        if (state.importBatch) {
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
            invalidateTransferCaches();
            appActions().refreshFiles();
        }
    });

    subscribeTransferEvent<[unknown, unknown, unknown]>("upload_error", (id, name, message) => {
        const uploadId = Number(id);
        if (!Number.isFinite(uploadId)) return;
        if (state.importBatch) {
            if (!state.cancelingUpload) recordImportFailureReason(name, message);
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
        const errorBody = humanizeBackendError(message);
        if (errorBody && !state.cancelingUpload) {
            notify({
                level: 'error',
                title: filename ? `Couldn't upload "${filename}"` : 'Upload failed',
                body: errorBody,
            });
        }

        if (batchFinished) {
            invalidateTransferCaches();
            appActions().refreshFiles();
        }
    });

    subscribeTransferEvent("import_start", () => {
        importCompleteReceived = false;
        importFailureReasons.length = 0;
        state.importBatch = createImportProgress();
        pushTransferStart({ id: IMPORT_TRANSFER_ID, direction: 'up', name: 'Preparing import…', total: 0 });
    });

    // Live phase label: "Extracting backup.zip", "Adding Photos", etc.
    subscribeTransferEvent<[unknown]>("import_progress", (info) => {
        if (!state.importBatch) return;
        const payload = asObjectRecord(info);
        const label = String(payload.label ?? "").trim();
        if (label) updateTransferName({ id: IMPORT_TRANSFER_ID, direction: 'up', name: label });
    });

    // Folders done, uploads begin: now we know the real file count.
    subscribeTransferEvent<[unknown]>("import_uploading", (info) => {
        if (!state.importBatch) return;
        const payload = asObjectRecord(info);
        const files = Number(payload.files) || 0;
        state.importBatch = reduceImportProgress(state.importBatch, { total: files });
        updateTransferName({
            id: IMPORT_TRANSFER_ID,
            direction: 'up',
            name: files > 0 ? `Importing 0 / ${files}` : 'Finishing import…',
        });
        refreshImportRow();
    });

    subscribeTransferEvent<[unknown]>("import_upload_progress", (info) => {
        if (!state.importBatch) return;
        state.importBatch = reduceImportProgress(state.importBatch, asObjectRecord(info));
        refreshImportRow();
    });

    subscribeTransferEvent<[unknown]>("import_complete", (info) => {
        importCompleteReceived = true;
        const payload = asObjectRecord(info);
        const failedUploads = Math.max(Number(payload.failed) || 0, state.importBatch?.failed || 0);
        const uploaded = Number(payload.uploaded) || 0;
        const oversize = Number(payload.oversize) || 0;
        const backendStatus = String(payload.status ?? '').toLowerCase();
        const fatalError = String(payload.error ?? '').trim();
        const reportedErrors = Number(payload.errorCount);
        const errorCount = Number.isFinite(reportedErrors)
            ? Math.max(0, Math.floor(reportedErrors))
            : (Array.isArray(payload.errors) ? payload.errors.length : 0);
        const canceled = state.cancelingUpload || backendStatus === 'canceled';
        const fatal = backendStatus === 'failed';
        const status = canceled
            ? 'canceled'
            : (fatal || failedUploads > 0 ? 'failed' : 'done');
        const resultLabel = formatImportResultLabel(uploaded, failedUploads);
        if (!state.importBatch) {
            pushTransferStart({ id: IMPORT_TRANSFER_ID, direction: 'up', name: 'Import completed', total: uploaded + failedUploads });
        }
        updateTransferName({
            id: IMPORT_TRANSFER_ID,
            direction: 'up',
            name: canceled
                ? 'Import canceled'
                : (fatal
                    ? 'Import failed'
                    : resultLabel),
        });
        markTransferDone({ id: IMPORT_TRANSFER_ID, direction: 'up', status });
        state.importBatch = null;

        invalidateTransferCaches();
        appActions().refreshFiles();

        if (!canceled && (status === 'failed' || failedUploads > 0 || oversize > 0 || errorCount > 0)) {
            const bits: string[] = [];
            if (failedUploads > 0) bits.push(`${failedUploads} failed`);
            if (oversize > 0) bits.push(`${oversize} skipped (too large)`);
            if (errorCount > 0) bits.push(`${errorCount} ${errorCount === 1 ? 'item' : 'items'} had errors`);
            // Include the first few concrete reasons (per-file upload errors,
            // then backend scan errors) so the summary is actionable, not just
            // counts.
            const reasons: string[] = [];
            if (fatalError) reasons.push(fatalError);
            for (const reason of importFailureReasons) {
                if (reasons.length >= MAX_IMPORT_FAILURE_REASONS) break;
                if (!reasons.includes(reason)) reasons.push(reason);
            }
            if (Array.isArray(payload.errors)) {
                for (const raw of payload.errors) {
                    if (reasons.length >= MAX_IMPORT_FAILURE_REASONS) break;
                    const text = String(raw ?? '').trim();
                    if (text && !reasons.includes(text)) reasons.push(text);
                }
            }
            notify({
                level: status === 'failed' ? 'error' : 'info',
                title: fatal
                    ? 'Import failed'
                    : resultLabel,
                body: [...bits, ...reasons].join('  ·  ') || 'The import stopped before it could finish.',
            });
        }
        importFailureReasons.length = 0;
    });
}

// uploadWithParentID opens the file picker and routes the selection through the
// shared import flow.
export async function uploadWithParentID(parentID: string) {
    const paths = await selectFiles();
    await runImportFlow(parentID, paths);
}

// importFolderWithParentID opens the directory picker and imports the chosen
// folder tree into parentID.
export async function importFolderWithParentID(parentID: string) {
    let dir = "";
    try {
        dir = await selectFolder();
    } catch (err) {
        console.error("SelectFolder failed:", err);
        notify({ level: 'error', title: 'Could not open the folder picker', body: humanizeBackendError(err) });
        return;
    }
    if (!dir) return;
    await runImportFlow(parentID, [dir]);
}

// runImportFlow is the single entry point for any selection (file picker, folder
// picker, or drag-drop). A plain-files selection keeps the original per-file
// upload UX; a selection containing folders or archives goes through the import
// dialog and the aggregated import flow.
async function runImportFlow(parentID: string, paths: string[]) {
    if (!paths.length) return;
    if (flowBusy) {
        notify({ level: 'info', title: 'A transfer is already in progress', body: 'Wait for it to finish, then start another.' });
        return;
    }
    flowBusy = true;
    activeTransferDriveId = state.activeChannel?.id ?? null;
    try {
        const onPersonal = state.activeChannel?.kind === 'personal';
        if (onPersonal) {
            // Refresh the snapshot so the modal's follow-up steps see truth.
            await loadEncryptionStatus();
        }

        let plan: ImportPlan | null = null;
        try {
            plan = await planImport(paths, false, false);
        } catch (err) {
            console.error("PlanImport failed:", err);
        }
        if (!plan) {
            // Don't fall through to the plain uploader: a directory path would be
            // sent to UploadToDriveFS and fail. Surface it and stop.
            notify({ level: 'error', title: 'Could not read the selection', body: 'Please try again.' });
            return;
        }

        if (plan.limitExceeded) {
            const maxItems = Math.max(1, Math.floor(Number(plan.maxItems) || 10_000));
            notify({
                level: 'info',
                title: 'Selection is too large',
                body: `Keep it under ${maxItems.toLocaleString()} items by removing generated/cache folders or splitting it into smaller batches.`,
            });
            return;
        }

        const complex = Number(plan.folders) > 0 || Number(plan.archives) > 0;

        if (!complex) {
            // Plain files: the original flow (per-file rows, encrypt-options modal).
            let encrypt = false;
            if (onPersonal) {
                const choice = await openUploadOptionsModal({ count: plan.files || paths.length });
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
            replan: (encrypt: boolean, extract: boolean) => planImport(paths, encrypt, extract),
        });
        if (!choice) return;

        const { encrypt, extract } = choice;
        if (encrypt && !state.encryption.passwordRemembered) {
            const ok = state.encryption.passwordSet
                ? await openEncryptionPasswordModal()
                : await openEncryptionSetupModal();
            if (!ok) return;
        }

        setTransferDirectionActive('upload', true);
        importCompleteReceived = false;
        let importThrew = false;
        try {
            const result = await importPaths(paths, parentID, encrypt, extract);
            if (!result.ok) {
                if (result.error.code === 'canceled') state.cancelingUpload = true;
                throw result.error;
            }
        } catch (error) {
            importThrew = true;
            console.error('Import failed:', error);
            if (!state.cancelingUpload && !importCompleteReceived) {
                notify({ level: 'error', title: 'Import failed', body: humanizeBackendError(error) });
            }
            if (!importCompleteReceived && !state.importBatch) {
                pushTransferStart({ id: IMPORT_TRANSFER_ID, direction: 'up', name: 'Import failed', total: 0 });
            }
        } finally {
            setTransferDirectionActive('upload', false);
            // import_complete is authoritative; if it never arrived (error or a
            // dropped event), finalize the aggregate row here with the right status.
            if (state.importBatch) {
                markTransferDone({ id: IMPORT_TRANSFER_ID, direction: 'up', status: importThrew ? 'failed' : 'done' });
                state.importBatch = null;
            }
            if (importThrew && !importCompleteReceived) {
                markTransferDone({ id: IMPORT_TRANSFER_ID, direction: 'up', status: 'failed' });
            }
        }
    } finally {
        if (!importCompleteReceived) invalidateTransferCaches();
        flowBusy = false;
        state.cancelingUpload = false;
    }
}

// uploadPathsBatch runs the classic per-file upload (one bell row per file).
async function uploadPathsBatch(paths: string[], parentID: string, encrypt: boolean) {
    if (activeTransferDriveId === null) activeTransferDriveId = state.activeChannel?.id ?? null;
    setTransferDirectionActive('upload', true);
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



    let uploadThrew = false;
    try {
        const parentIDs = paths.map(() => parentID);
        const upload = await uploadToDriveFs(paths, parentIDs, encrypt);
        if (!upload.result.ok) {
            if (upload.result.error.code === 'canceled') state.cancelingUpload = true;
            throw upload.result.error;
        }
    } catch (error) {
        uploadThrew = true;
        console.error('Upload failed:', error);
        if (!state.cancelingUpload) {
            notify({ level: 'error', title: 'Upload failed', body: humanizeBackendError(error) });
        }
    } finally {
        setTransferDirectionActive('upload', false);
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
        invalidateTransferCaches();
        state.uploadBatch = null;
        state.cancelingUpload = false;
    }
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
    // Modern WebKit rejects unhandled page drags, and WebView2 reports native
    // paths through Wails. Register the native drop target before the event.
    onNativeFileDrop(() => {});
    subscribeTransferEvent<[unknown]>('files_dropped', (payload) => {
        // If an in-app drag-to-move is underway, ignore native drops entirely
        // (macOS can still fire one for the internal drag).
        if (state.dragState) return;
        const event = asObjectRecord(payload);
        const rawPaths = Array.isArray(payload) ? payload : event.paths;
        if (!Array.isArray(rawPaths) || !rawPaths.length) return;
        const paths = rawPaths.filter((path): path is string => typeof path === 'string');
        if (!paths.length || !state.activeChannel) return;
        const x = Number(event.x);
        const y = Number(event.y);
        if (!Number.isFinite(x) || !Number.isFinite(y)) return;
        const target = document.elementFromPoint(x, y);
        if (!target || !(target as HTMLElement).closest('#file-list')) return;
        void runImportFlow(state.currentFolderId, paths);
    });
}
