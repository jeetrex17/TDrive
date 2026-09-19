// Upload/Download progress handling for TDrive frontend.
//
// All transfer state surfaces through the notification bell — there is no
// separate transfer pill or sheet anymore. Active transfers feed the bell's
// hover popover via pushTransferStart/updateTransferProgress/markTransferDone.
// Completed transfers stay in the bell's "Recent" panel until cleared.

import { invalidateFolderIndex, state, setTransferDirectionActive, type DownloadQueueItem } from '../state';
import { createFolder, downloadFile, downloadFolder, importPaths, isAndroidPlatform, isMobilePlatform, onRuntimeEvent, planImport, selectFiles, selectFolder, uploadToDriveFs, type RuntimeEventMap, type RuntimeUnsubscribe } from '../api';
import { canSaveToDownloads, saveToDownloads } from './android-downloads';
import { rememberDownloadSharePath } from '../ui/mobile/mobile-shell-store';
import type { ImportPlan, OperationError } from '../types';
import type { TransferEvent } from '../ui/notifications/notif-store';
import { dismissNotification, notify } from './notifications';
import {
    canPickFolder,
    folderPathFor,
    folderPathsFor,
    materializeAndroidFiles,
    pickAndroidFolder,
    releaseAndroidFiles,
    type AndroidFolderFile,
    type AndroidFolderManifest,
} from './android-folder';
import { humanizeBackendError } from './errors';
import { appActions } from './app-actions';
import { loadEncryptionStatus } from './encryption';
import { openUploadOptionsModal } from './modals/upload-options';
import { activateFileSelectionProgress } from './file-selection';
import { openImportOptionsModal } from './modals/import-options';
import { openEncryptionSetupModal } from './modals/encryption-setup';
import { openEncryptionPasswordModal } from './modals/encryption-password';
import { createImportProgress, reduceImportProgress } from './import-progress';
import { TransferBatch, type UploadOutcome } from './transfer-batch';
import { activateTransferPersistence } from './transfer-persistence';
import {
    pushQueuedTransfer,
    pushTransferStart,
    updateTransferProgress,
    updateTransferName,
    markTransferDone,
    setTransferNote,
    wasUploadCanceled,
} from './notif-bell';


let transferUnsubscribers: RuntimeUnsubscribe[] = [];
let stopTransferPersistence: (() => void) | null = null;
let downloadRequestSequence = 0;

function subscribeTransferEvent<K extends keyof RuntimeEventMap>(
    eventName: K,
    callback: (...data: RuntimeEventMap[K]) => void,
): RuntimeUnsubscribe {
    const unsubscribe = onRuntimeEvent(eventName, callback);
    transferUnsubscribers.push(unsubscribe);
    return unsubscribe;
}




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
    request_id?: unknown;
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


function activateDownloadProgressEvents(): void {
    subscribeTransferEvent("download_progress", (percent, requestId) => {
        const activeKey = state.activeDownloadId;
        if (activeKey === null || String(requestId ?? '') !== state.activeDownloadRequestId) return;
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

    subscribeTransferEvent("folder_download_progress", (rawPayload) => {
        const activeKey = state.activeDownloadId;
        if (activeKey === null) return;
        const item = state.downloadQueue.find((entry) => entry.key === activeKey);
        const payload = asObjectRecord(rawPayload) as FolderDownloadProgressPayload;
        if (
            !item
            || item.kind !== 'folder'
            || String(payload?.request_id ?? '') !== state.activeDownloadRequestId
            || String(payload?.folder_id ?? '') !== item.id
        ) return;

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

/**
 * Records where a phone download went, after putting it somewhere they can
 * actually get to.
 *
 * The answer is kept on the transfer rather than raised as a toast. A phone has
 * no Finder to go and look in, so "where is it" is the whole point of the
 * download -- and a toast says it once, to someone who may be watching the
 * progress bar rather than the bottom of the screen, and cannot be asked again.
 * The row keeps it for as long as the row is there.
 *
 * The two platforms need opposite things. iOS has no shared storage, so the
 * download stays in the app container and the answer is that the container is
 * published to the Files app; the share sheet Go opens afterwards is for
 * sending it on, not for finding it. Android has a real public Downloads
 * folder but hides the sandbox completely, so the file has to be moved out
 * before it exists as far as the user is concerned.
 */
async function announceMobileDownload(item: DownloadQueueItem, savedPath: string): Promise<void> {
    const folder = item.kind === 'folder';
    const note = (text: string) => setTransferNote({ id: item.key, direction: 'down', note: text });

    if (isAndroidPlatform() && canSaveToDownloads()) {
        try {
            const location = await saveToDownloads(savedPath);
            note(location ? `Saved to ${location}` : 'Saved to your Downloads folder');
            return;
        } catch (err) {
            // The bytes arrived either way, so this is a question of where they
            // are rather than a failed transfer -- but it is the one outcome
            // here the user has to act on, so it is still worth interrupting for.
            console.error('Could not move the download to Downloads:', err);
            note('Saved inside TDrive, not in your Downloads folder');
            notify({
                level: 'warning',
                title: 'Could not reach your Downloads folder',
                body: `${item.name} is saved inside TDrive instead. Open Transfers to share it out.`,
            });
            return;
        }
    }

    // Go opens the share sheet after a single file; keep the path so the
    // Transfers tab can offer it again later. A folder has no share sheet --
    // no phone share sheet takes a directory -- so Files is the only route.
    if (!folder && savedPath) rememberDownloadSharePath(`xfer:down:${item.key}`, savedPath);
    note(folder
        ? 'Saved to Files › On My iPhone › TDrive › Downloads'
        : 'Saved to Files › TDrive › Downloads');
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
    const requestId = `${next.key}@${++downloadRequestSequence}`;
    state.activeDownloadRequestId = requestId;
    replaceDownloadItem(next.key, (current) => ({
        ...current,
        state: 'downloading',
        progress: Math.max(0, Math.min(100, Number(current.progress) || 0)),
    }));
    pushTransferStart({ id: next.key, direction: 'down', name: next.name, total: next.size });

    try {
        let result = await dispatchDownload(next, requestId);
        if (!result.result.ok && result.result.error.code === 'encryption_password_required') {
            const unlocked = await openEncryptionPasswordModal();
            if (!unlocked) {
                finalizeDownload(next.key, 'canceled');
                return;
            }
            result = await dispatchDownload(next, requestId);
        }

        if (result.result.ok) {
            finalizeDownload(next.key, 'done');
            if (isMobilePlatform()) await announceMobileDownload(next, result.savedPath);
            else if (next.kind === 'folder') {
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
        state.activeDownloadRequestId = null;
        void startNextDownload();
    }
}

export function enqueueDownload(id: unknown, name: unknown, size: unknown, sourceChannelId?: unknown) {
    const downloadId = Number(id);
    if (!Number.isSafeInteger(downloadId) || downloadId <= 0) return;
    const channelId = downloadChannelId(sourceChannelId);
    if (channelId === null) return;
    const label = String(name || "Download");
    enqueueDownloadItem({
        key: downloadQueueKey('file', channelId, String(downloadId)),
        channelId,
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

export function enqueueFolderDownload(id: unknown, name: unknown, size: unknown = 0, sourceChannelId?: unknown) {
    const folderId = String(id ?? '').trim();
    if (!folderId) return;
    const channelId = downloadChannelId(sourceChannelId);
    if (channelId === null) return;
    const storedSize = Math.max(0, Number(size) || 0);
    enqueueDownloadItem({
        key: downloadQueueKey('folder', channelId, folderId),
        channelId,
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
    // The bell is the user-visible transfer queue. Use the same drive-scoped
    // id the scheduler will later promote, so two equal message ids from
    // separate drives cannot overwrite one another.
    if (!existing) {
        pushQueuedTransfer({ id: item.key, direction: 'down', name: item.name, total: item.size });
    }
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
    // The row keeps the reason; the toast is the transient copy of it.
    if (reason) setTransferNote({ id: item.key, direction: 'down', note: reason });
    if (typeof error !== 'string' && error.code === 'already_exists') {
        notify({ level: 'warning', title: noun + ' already exists', body: reason, history: false });
        return;
    }
    // The fields are read out now rather than closed over: finalizeDownload
    // drops the item from the queue right after this, and a Retry that
    // referenced a removed entry would do nothing.
    const { kind, id, name, size, channelId } = item;
    const toastId = `download-retry:${item.key}`;
    notify({
        id: toastId,
        level: 'error',
        title: `Couldn't download ${item.name}`,
        body: reason || 'The download could not be completed.',
        history: false,
        action: {
            label: 'Retry',
            // One shot. The toast is sticky, so without this it sits there
            // offering to retry a download that is already running again, and
            // a second tap re-queues a key that is mid-flight -- which resets
            // the queue entry's progress under it.
            run: oneShot(() => {
                dismissNotification(toastId);
                if (kind === 'folder') enqueueFolderDownload(id, name, size, channelId);
                else enqueueDownload(id, name, size, channelId);
            }),
        },
    });
}

/**
 * The retry for a failed transfer row, or undefined when there is not one.
 *
 * Only downloads can be retried: everything a download needs is in the row
 * itself, while an upload's source path is long gone by the time its row is on
 * screen. Owning the id parse here keeps it next to the code that builds the
 * key -- the Transfers tab only knows it is holding a failed transfer.
 *
 * Re-queuing reuses the same key, so the failed row turns back into a running
 * one in place rather than leaving a dead twin behind it.
 */
export function downloadRetryFor(transfer: TransferEvent): (() => void) | undefined {
    if (transfer.status !== 'failed' || transfer.direction !== 'down') return undefined;
    if (!transfer.id.startsWith('xfer:down:')) return undefined;
    const key = transfer.id.slice('xfer:down:'.length);
    const { name, total } = transfer;
    const parsed = parseDownloadQueueKey(key);
    if (parsed?.kind === 'file') {
        const id = Number(parsed.id);
        return Number.isFinite(id) ? () => enqueueDownload(id, name, total, parsed.channelId) : undefined;
    }
    if (parsed?.kind === 'folder') return () => enqueueFolderDownload(parsed.id, name, total, parsed.channelId);
    return undefined;
}

/** Wraps a toast action so it runs at most once, however often it is tapped. */
function oneShot(run: () => void): () => void {
    let spent = false;
    return () => {
        if (spent) return;
        spent = true;
        run();
    };
}

function dispatchDownload(item: DownloadQueueItem, requestId: string) {
    return item.kind === 'folder'
        ? downloadFolder(item.channelId, item.id, requestId)
        : downloadFile(item.channelId, item.id, item.id, requestId);
}

type DownloadQueueKind = DownloadQueueItem['kind'];

function downloadChannelId(sourceChannelId: unknown): number | null {
    const channelId = sourceChannelId === undefined
        ? Number(state.activeChannel?.id)
        : Number(sourceChannelId);
    return Number.isSafeInteger(channelId) && channelId > 0 ? channelId : null;
}

/** A job identity must include its drive: Telegram message ids repeat per channel. */
function downloadQueueKey(kind: DownloadQueueKind, channelId: number, id: string): string {
    return `${kind}:${channelId}:${id}`;
}

function parseDownloadQueueKey(key: string): { kind: DownloadQueueKind; channelId: number; id: string } | null {
    const firstColon = key.indexOf(':');
    if (firstColon < 1) return null;
    const kind = key.slice(0, firstColon);
    if (kind !== 'file' && kind !== 'folder') return null;
    const rest = key.slice(firstColon + 1);
    const secondColon = rest.indexOf(':');
    // Transfers created before drive-scoped keys shipped did not carry their
    // source. Keep their Retry button useful by associating it with the drive
    // that is selected at retry time; all newly-created work takes the branch
    // below and remains bound to its original source.
    if (secondColon < 1 || !/^\d+$/.test(rest.slice(0, secondColon))) {
        const channelId = downloadChannelId(undefined);
        return channelId === null || !rest ? null : { kind, channelId, id: rest };
    }
    const channelId = Number(rest.slice(0, secondColon));
    const id = rest.slice(secondColon + 1);
    if (!Number.isSafeInteger(channelId) || channelId <= 0 || !id) return null;
    return { kind, channelId, id };
}

function clampFinite(raw: unknown, fallback: number, minValue: number, maxValue: number): number {
    const value = Number(raw);
    if (!Number.isFinite(value)) return fallback;
    return Math.max(minValue, Math.min(maxValue, value));
}

// IMPORT_TRANSFER_ID keys the single aggregate bell row for a folder/archive
// import. It is a string so it never collides with numeric per-file upload ids.
const IMPORT_TRANSFER_ID = 'import';

// UPLOAD_BATCH_TRANSFER_ID keys the same kind of row for a multi-file upload.
// One row per batch rather than one per file: a two-hundred-file selection used
// to push two hundred rows, which told the reader nothing about the batch and
// pushed everything else in the bell past its hundred-entry cap.
const UPLOAD_BATCH_TRANSFER_ID = 'upload-batch';

// The aggregate for the batch currently uploading, or null when files are
// uploading one at a time and each keeps its own row.
let uploadBatch: TransferBatch | null = null;

// flowBusy serializes import/upload flows: a second trigger (rapid menu clicks,
// or a drop during an active import) is rejected rather than corrupting the
// shared batch/transfer state.
let flowBusy = false;

/**
 * Runs a transfer flow under that lock, or declines if one is already running.
 *
 * Every entry point needs the same four lines, and one of them used to take the
 * lock a beat too late: the file picker only claimed it once the selection had
 * come back, so during the seconds a phone spends copying the picked documents
 * nothing was held at all. Tapping Upload again -- which is exactly what a
 * reader does when the screen has not moved -- opened a second picker on top of
 * the first. Held from the trigger, that window is covered too.
 */
async function withTransferFlow(run: () => Promise<void>): Promise<void> {
    if (flowBusy) {
        notify({
            level: 'info',
            title: 'A transfer is already in progress',
            body: 'Wait for it to finish, then start another.',
        });
        return;
    }
    flowBusy = true;
    try {
        await run();
    } finally {
        flowBusy = false;
    }
}
// First few distinct per-file failure reasons, folded into the aggregate
// import summary toast. Imports report one toast for the whole batch, so this
// is the only place the backend's actual error text survives to the user.
const MAX_IMPORT_FAILURE_REASONS = 3;
const importFailureReasons: string[] = [];
// The same for a multi-file upload, which reports one summary for the batch:
// two hundred files failing the same way must not raise two hundred toasts.
const uploadBatchFailureReasons: string[] = [];
let importCompleteReceived = false;

/** Keeps the first few distinct reasons and drops the rest, so the log is bounded. */
function recordFailureReason(into: string[], name: unknown, message: unknown) {
    if (into.length >= MAX_IMPORT_FAILURE_REASONS) return;
    const reason = String(message ?? '').trim();
    if (!reason) return;
    const fname = String(name ?? '').trim();
    const entry = fname ? `${fname}: ${reason}` : reason;
    if (!into.includes(entry)) into.push(entry);
}

function formatImportResultLabel(uploaded: number, failedUploads = 0) {
    const imported = uploaded === 1 ? 'Imported 1 file' : `Imported ${uploaded} files`;
    return failedUploads > 0 ? `${imported} · ${failedUploads} failed` : imported;
}

// refreshImportRow renders one constant-size aggregate supplied by the backend.
// A failed file counts as settled: it is not going to move again, and a bar
// that stops short because three files failed is the less useful answer.
function refreshImportRow() {
    const batch = state.importBatch;
    if (!batch) return;
    updateTransferProgress({
        id: IMPORT_TRANSFER_ID,
        direction: 'up',
        progress: batch.progress,
        bytes: batch.bytes,
        itemsDone: batch.done + batch.failed,
        itemsTotal: batch.total,
        items: batch.active,
        itemsActive: batch.activeCount,
    });
}

// refreshUploadBatchRow renders the frontend's own aggregate for a multi-file
// upload. Same shape, same bound; see modules/transfer-batch.ts.
function refreshUploadBatchRow() {
    if (!uploadBatch) return;
    const snapshot = uploadBatch.snapshot();
    updateTransferProgress({
        id: UPLOAD_BATCH_TRANSFER_ID,
        direction: 'up',
        progress: snapshot.progress,
        bytes: snapshot.bytes,
        itemsDone: snapshot.itemsDone,
        itemsTotal: snapshot.itemsTotal,
        items: snapshot.items,
        itemsActive: snapshot.itemsActive,
    });
}

/** "Uploaded 197 files · 2 failed", the sentence the finished row keeps. */
function formatUploadBatchLabel(batch: TransferBatch): string {
    const uploaded = batch.succeeded === 1 ? 'Uploaded 1 file' : `Uploaded ${batch.succeeded} files`;
    const parts = [uploaded];
    if (batch.failures > 0) parts.push(`${batch.failures} failed`);
    if (batch.stopped > 0) parts.push(`${batch.stopped} canceled`);
    return parts.join(' · ');
}

/**
 * Folds one file's outcome into the batch aggregate and redraws its row.
 * Returns false when no batch is running, so the caller keeps its own row.
 */
function settleUploadInBatch(uploadId: number, outcome: UploadOutcome): boolean {
    if (!uploadBatch) return false;
    uploadBatch.settle(uploadId, outcome);
    refreshUploadBatchRow();
    return true;
}

/**
 * Closes the aggregate row once the upload call has returned, and reports the
 * per-file failures it folded away.
 *
 * One summary rather than one toast per file: a batch is the case where the
 * same failure can land two hundred times, and two hundred toasts saying it
 * would bury the one thing the reader could do about it.
 */
function finalizeUploadBatch(uploadThrew: boolean): void {
    const batch = uploadBatch;
    if (!batch) return;
    const canceled = state.cancelingUpload;
    batch.settleRemaining(canceled ? 'canceled' : uploadThrew ? 'failed' : 'done');
    refreshUploadBatchRow();
    uploadBatch = null;

    updateTransferName({
        id: UPLOAD_BATCH_TRANSFER_ID,
        direction: 'up',
        name: canceled ? 'Upload canceled' : formatUploadBatchLabel(batch),
    });
    // A call that threw has already raised its own toast, with the Retry that
    // restarts the whole batch; this is for the files that failed inside a
    // call that otherwise succeeded, which nothing else would mention. The
    // reasons stay on the row.
    const batchReasons = !canceled && !uploadThrew && batch.failures > 0
        ? uploadBatchFailureReasons.map(humanizeBackendError).join('\n') || 'The rest of the batch finished.'
        : '';
    if (batchReasons) setTransferNote({ id: UPLOAD_BATCH_TRANSFER_ID, direction: 'up', note: batchReasons });
    markTransferDone({
        id: UPLOAD_BATCH_TRANSFER_ID,
        direction: 'up',
        status: canceled ? 'canceled' : (uploadThrew || batch.failures > 0) ? 'failed' : 'done',
    });
    appActions().refreshFiles();

    if (batchReasons) {
        notify({
            level: 'error',
            title: batch.failures === 1 ? "Couldn't upload 1 file" : `Couldn't upload ${batch.failures} files`,
            body: batchReasons,
            history: false,
        });
    }
}

function activateUploadProgressEvents(): void {
    subscribeTransferEvent("upload_start", (id, name, size, parentId) => {
        // New backends suppress detailed events during imports. Ignore any
        // strays from an older backend so a large import still keeps one row.
        if (state.importBatch) {
            return;
        }

        const uploadId = Number(id);
        if (!Number.isFinite(uploadId)) return;
        const filename = String(name ?? "");

        // In a batch the file joins the aggregate row instead of raising one of
        // its own, and nothing per-file is retained once it finishes.
        if (uploadBatch) {
            uploadBatch.start(uploadId, filename, Number(size) || 0);
            refreshUploadBatchRow();
            return;
        }

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

    subscribeTransferEvent("upload_progress", (id, percent) => {
        const uploadId = Number(id);
        if (!Number.isFinite(uploadId)) return;
        const value = Number(percent);
        if (!Number.isFinite(value)) return;
        const clamped = Math.max(0, Math.min(100, value));

        if (state.importBatch) {
            return;
        }
        if (uploadBatch) {
            uploadBatch.progress(uploadId, clamped);
            refreshUploadBatchRow();
            return;
        }

        const item = state.uploadTransfers.get(uploadId);
        if (!item) return;
        item.progress = clamped;
        updateTransferProgress({ id: uploadId, direction: 'up', progress: clamped });
    });

    subscribeTransferEvent("upload_complete", (id, name) => {
        const uploadId = Number(id);
        if (!Number.isFinite(uploadId)) return;
        if (state.importBatch) {
            return;
        }
        if (!settleUploadInBatch(uploadId, 'done')) {
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
            markTransferDone({ id: uploadId, direction: 'up', status: 'done' });
        }

        if (state.uploadBatch) state.uploadBatch.done += 1;
        const batchFinished = Boolean(state.uploadBatch && state.uploadBatch.done + state.uploadBatch.failed >= state.uploadBatch.total);
        if (batchFinished) state.uploadBatch = null;

        if (batchFinished) {
            invalidateTransferCaches();
            appActions().refreshFiles();
        }
    });

    subscribeTransferEvent("upload_error", (id, name, message) => {
        const uploadId = Number(id);
        if (!Number.isFinite(uploadId)) return;
        if (state.importBatch) {
            if (!state.cancelingUpload) recordFailureReason(importFailureReasons, name, message);
            return;
        }
        const filename = String(name ?? "");
        // A file the user stopped from its own row is already marked canceled;
        // the backend reports it through this same error event, and neither the
        // row nor a toast should call it a failure.
        const canceled = state.cancelingUpload || wasUploadCanceled(uploadId);

        if (uploadBatch) {
            settleUploadInBatch(uploadId, canceled ? 'canceled' : 'failed');
            if (!canceled) recordFailureReason(uploadBatchFailureReasons, filename, message);
            if (state.uploadBatch) state.uploadBatch.failed += 1;
            return;
        }

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
        // The reason stays on the row, where it can be read again after the
        // toast has gone. The toast itself is not mirrored into the bell: that
        // listed one failure twice and counted it twice.
        const errorBody = humanizeBackendError(message);
        if (errorBody && !canceled) setTransferNote({ id: uploadId, direction: 'up', note: errorBody });
        markTransferDone({ id: uploadId, direction: 'up', status: canceled ? 'canceled' : 'failed' });
        if (errorBody && !canceled) {
            notify({
                level: 'error',
                title: filename ? `Couldn't upload "${filename}"` : 'Upload failed',
                body: errorBody,
                history: false,
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
    subscribeTransferEvent("import_progress", (info) => {
        if (!state.importBatch) return;
        const payload = asObjectRecord(info);
        const label = String(payload.label ?? "").trim();
        if (label) updateTransferName({ id: IMPORT_TRANSFER_ID, direction: 'up', name: label });
    });

    // Folders done, uploads begin: now we know the real file count.
    subscribeTransferEvent("import_uploading", (info) => {
        if (!state.importBatch) return;
        const payload = asObjectRecord(info);
        const files = Number(payload.files) || 0;
        state.importBatch = reduceImportProgress(state.importBatch, { total: files });
        // The counts live in the row's own detail line from here on, so the
        // title stays put instead of being rewritten on every tick.
        updateTransferName({
            id: IMPORT_TRANSFER_ID,
            direction: 'up',
            name: files > 0 ? `Importing ${files} ${files === 1 ? 'file' : 'files'}` : 'Finishing import…',
        });
        refreshImportRow();
    });

    subscribeTransferEvent("import_upload_progress", (info) => {
        if (!state.importBatch) return;
        state.importBatch = reduceImportProgress(state.importBatch, asObjectRecord(info));
        refreshImportRow();
    });

    subscribeTransferEvent("import_complete", (info) => {
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
    // The lock covers the picker, not just what follows it: on a phone the
    // host spends seconds copying the picked documents before this resolves,
    // and that window used to be unguarded. See withTransferFlow.
    await withTransferFlow(async () => {
        const paths = await selectFiles();
        await importSelection(parentID, paths);
    });
}

// importFolderWithParentID opens the directory picker and imports the chosen
// folder tree into parentID.
export async function importFolderWithParentID(parentID: string) {
    // The lock is taken here rather than inside either branch, so it covers the
    // picker itself -- the same window the file picker was leaving open.
    await withTransferFlow(async () => {
        // Android's picker answers through the app's own bridge with a manifest
        // rather than a path, because Wails will not hand a document tree to its
        // dialog API and because nothing has been copied out of the tree yet.
        if (canPickFolder()) {
            await importAndroidFolder(parentID);
            return;
        }
        let dir = "";
        try {
            dir = await selectFolder();
        } catch (err) {
            console.error("SelectFolder failed:", err);
            notify({ level: 'error', title: 'Could not open the folder picker', body: humanizeBackendError(err) });
            return;
        }
        if (!dir) return;
        await importSelection(parentID, [dir]);
    });
}

// Peak cache use is one window, which is the whole point: a handful of files
// instead of the entire folder. Four also keeps the uploader busy, since a
// batch goes up concurrently, so the copy the next window pays for is a small
// share of the time rather than a stall between every single file.
const ANDROID_UPLOAD_WINDOW = 4;

// importAndroidFolder picks a folder and uploads it without ever copying the
// whole tree into the cache.
async function importAndroidFolder(parentID: string) {
    activeTransferDriveId = state.activeChannel?.id ?? null;
    // The picker covers the app while it is open, so this row is only seen once
    // it closes, which is exactly when the tree walk is still running and the
    // screen would otherwise look frozen. Document-tree walks are slow enough
    // on a deep folder for that to be seconds.
    pushTransferStart({ id: IMPORT_TRANSFER_ID, direction: 'up', name: 'Reading folder…', total: 0 });
    try {
        let manifest: AndroidFolderManifest | null = null;
        try {
            manifest = await pickAndroidFolder();
        } catch (err) {
            console.error("PickFolder failed:", err);
            setTransferNote({ id: IMPORT_TRANSFER_ID, direction: 'up', note: humanizeBackendError(err) });
            markTransferDone({ id: IMPORT_TRANSFER_ID, direction: 'up', status: 'failed' });
            notify({ level: 'error', title: 'Could not open the folder picker', body: humanizeBackendError(err), history: false });
            return;
        }
        if (!manifest) {
            // Changing your mind is not an error and must not read as one.
            markTransferDone({ id: IMPORT_TRANSFER_ID, direction: 'up', status: 'canceled' });
            return;
        }
        await runAndroidImport(parentID, manifest);
    } catch (err) {
        // The row above is already on screen and would spin forever if one of
        // the confirmation steps threw. markTransferDone leaves an entry that
        // already ended alone, so this never rewrites a real outcome.
        console.error('Android folder import failed:', err);
        setTransferNote({ id: IMPORT_TRANSFER_ID, direction: 'up', note: humanizeBackendError(err) });
        markTransferDone({ id: IMPORT_TRANSFER_ID, direction: 'up', status: 'failed' });
        notify({ level: 'error', title: 'Import failed', body: humanizeBackendError(err), history: false });
    } finally {
        state.cancelingUpload = false;
    }
}

// runAndroidImport recreates the manifest's folder tree, then uploads its files
// a window at a time so the cache never holds more than one window.
async function runAndroidImport(parentID: string, manifest: AndroidFolderManifest) {
    const folderPaths = folderPathsFor(manifest);
    const totalFiles = manifest.files.length;
    const totalBytes = manifest.files.reduce((sum, file) => sum + file.size, 0);

    const onPersonal = state.activeChannel?.kind === 'personal';
    // Refresh the snapshot so the modal's follow-up steps see truth.
    if (onPersonal) await loadEncryptionStatus();

    const plan: ImportPlan = {
        files: totalFiles,
        folders: folderPaths.length,
        bytes: totalBytes,
        oversize: 0,
        archives: 0,
        ignored: 0,
        maxBytes: 0,
        maxItems: 0,
        limitExceeded: false,
        errorCount: 0,
        errors: [],
    };
    const choice = await openImportOptionsModal({
        plan,
        personal: onPersonal,
        hasArchives: false,
        // Nothing here moves with the options: there are no archives to
        // extract, and encrypting changes neither the file count nor which
        // folder a file lands in.
        replan: async () => plan,
    });
    if (!choice) {
        markTransferDone({ id: IMPORT_TRANSFER_ID, direction: 'up', status: 'canceled' });
        return;
    }
    if (choice.encrypt && !state.encryption.passwordRemembered) {
        const ok = state.encryption.passwordSet
            ? await openEncryptionPasswordModal()
            : await openEncryptionSetupModal();
        if (!ok) {
            markTransferDone({ id: IMPORT_TRANSFER_ID, direction: 'up', status: 'canceled' });
            return;
        }
    }

    importFailureReasons.length = 0;
    // The aggregate row is the only upload surface an import gets, and setting
    // this is what makes the per-file upload events fold into it.
    state.importBatch = reduceImportProgress(createImportProgress(), { total: totalFiles });
    setTransferDirectionActive('upload', true);
    updateTransferName({ id: IMPORT_TRANSFER_ID, direction: 'up', name: 'Creating folders…' });

    let done = 0;
    let failed = 0;
    let bytesDone = 0;
    const report = () => {
        const batch = state.importBatch;
        if (!batch) return;
        const next = reduceImportProgress(batch, {
            total: totalFiles,
            done,
            failed,
            progress: totalBytes > 0 ? (bytesDone / totalBytes) * 100 : 0,
        });
        state.importBatch = next;
        updateTransferProgress({
            id: IMPORT_TRANSFER_ID,
            direction: 'up',
            progress: next.progress,
            bytes: bytesDone,
            total: totalBytes,
            itemsDone: done + failed,
            itemsTotal: totalFiles,
        });
    };

    let fatalError = '';
    try {
        const folderIds = await createAndroidFolderTree(parentID, folderPaths);
        // Show the tree before the bytes arrive, so the user can navigate into
        // it while a long upload runs.
        invalidateTransferCaches();
        appActions().refreshFiles();
        updateTransferName({ id: IMPORT_TRANSFER_ID, direction: 'up', name: manifest.root || 'Folder' });
        report();

        for (let start = 0; start < totalFiles && !state.cancelingUpload; start += ANDROID_UPLOAD_WINDOW) {
            const batch = manifest.files.slice(start, start + ANDROID_UPLOAD_WINDOW);
            const outcome = await uploadAndroidWindow(batch, manifest.root, folderIds, parentID, choice.encrypt);
            done += outcome.done;
            failed += outcome.failed;
            // Count the window's bytes however it ended: the bar tracks work
            // gone through, and the failure count carries the outcome.
            bytesDone += batch.reduce((sum, file) => sum + file.size, 0);
            report();
        }
    } catch (err) {
        console.error('Android folder import failed:', err);
        fatalError = humanizeBackendError(err);
    } finally {
        setTransferDirectionActive('upload', false);
        finishAndroidImport(done, failed, fatalError);
    }
}

// createAndroidFolderTree maps every directory in the manifest to a drive
// folder id. folderPathsFor orders parents before children, so each lookup of
// a parent has already happened.
async function createAndroidFolderTree(parentID: string, folderPaths: string[]): Promise<Map<string, string>> {
    const folderIds = new Map<string, string>([['', parentID]]);
    for (const path of folderPaths) {
        if (state.cancelingUpload) break;
        const cut = path.lastIndexOf('/');
        const parent = folderIds.get(cut < 0 ? '' : path.slice(0, cut)) ?? parentID;
        const folder = await createFolder(path.slice(cut + 1), parent);
        folderIds.set(path, folder.id);
    }
    return folderIds;
}

// uploadAndroidWindow copies one window out of the document tree, uploads it,
// and releases it again.
async function uploadAndroidWindow(
    files: AndroidFolderFile[],
    root: string,
    folderIds: Map<string, string>,
    parentID: string,
    encrypt: boolean,
): Promise<{ done: number; failed: number }> {
    const ids = files.map((file) => file.id);
    try {
        const materialized = await materializeAndroidFiles(ids);
        const paths: string[] = [];
        const parentIDs: string[] = [];
        let unreadable = 0;
        for (const file of files) {
            const path = materialized.get(file.id);
            if (!path) {
                // One file the bridge could not read does not stop the window.
                unreadable += 1;
                recordFailureReason(importFailureReasons, file.rel, 'could not be read from the folder');
                continue;
            }
            paths.push(path);
            parentIDs.push(folderIds.get(folderPathFor(root, file.rel)) ?? parentID);
        }
        if (!paths.length) return { done: 0, failed: unreadable };

        const upload = await uploadToDriveFs(paths, parentIDs, encrypt);
        if (!upload.result.ok && upload.result.error.code === 'canceled') state.cancelingUpload = true;
        return { done: upload.files.length, failed: unreadable + paths.length - upload.files.length };
    } finally {
        // Cache stays bounded only if every window is released, including the
        // one that threw or was cancelled part way through. A release that
        // fails must not mask the upload error or stop the next window.
        try {
            await releaseAndroidFiles(ids);
        } catch (err) {
            console.error('ReleaseFiles failed:', err);
        }
    }
}

function finishAndroidImport(done: number, failed: number, fatalError: string) {
    const canceled = state.cancelingUpload;
    const resultLabel = formatImportResultLabel(done, failed);
    updateTransferName({
        id: IMPORT_TRANSFER_ID,
        direction: 'up',
        name: canceled ? 'Import canceled' : (fatalError ? 'Import failed' : resultLabel),
    });
    markTransferDone({
        id: IMPORT_TRANSFER_ID,
        direction: 'up',
        status: canceled ? 'canceled' : (fatalError || failed > 0 ? 'failed' : 'done'),
    });
    state.importBatch = null;
    invalidateTransferCaches();
    appActions().refreshFiles();

    if (!canceled && (fatalError || failed > 0)) {
        // The row carries only a status, so the reasons the backend gave for
        // individual files are the only actionable thing the user gets.
        const reasons = fatalError ? [fatalError, ...importFailureReasons] : [...importFailureReasons];
        const bits = failed > 0 ? [`${failed} failed`] : [];
        notify({
            level: 'error',
            title: fatalError ? 'Import failed' : resultLabel,
            body: [...bits, ...reasons.slice(0, MAX_IMPORT_FAILURE_REASONS)].join('  ·  ')
                || 'The import stopped before it could finish.',
        });
    }
    importFailureReasons.length = 0;
}

/**
 * What every selection ends up in, however it was chosen -- file picker, folder
 * picker or drag-drop. A plain-files selection keeps the original per-file
 * upload UX; a selection containing folders or archives goes through the import
 * dialog and the aggregated import flow.
 *
 * Callers hold the transfer lock. It is theirs rather than taken here because a
 * picker has to be covered from the moment it opens, which is well before it
 * has a selection to hand over; see withTransferFlow.
 */
async function importSelection(parentID: string, paths: string[]) {
    if (!paths.length) return;
    activeTransferDriveId = state.activeChannel?.id ?? null;
    try {
        const onPersonal = state.activeChannel?.kind === 'personal';

        // Both answers are wanted before the first modal and neither needs the
        // other, so they go together. Awaited one after the next, the encryption
        // snapshot -- which does not depend on the selection at all -- sat on
        // the critical path between the picker closing and the modal opening.
        let plan: ImportPlan | null = null;
        const [, planned] = await Promise.all([
            // Refresh the snapshot so the modal's follow-up steps see truth.
            onPersonal ? loadEncryptionStatus() : Promise.resolve(),
            planImport(paths, false, false).catch((err: unknown) => {
                console.error("PlanImport failed:", err);
                return null;
            }),
        ]);
        plan = planned;
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
            // Plain files: the flat uploader, behind the encrypt-options modal.
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
        state.cancelingUpload = false;
    }
}

/**
 * The Retry behind a failed upload toast. It re-enters the same batch, so it
 * takes the same lock the picker and the drop target do: starting an upload
 * while another is running cancels that one on the backend, and a retry is no
 * more entitled to do that than any other trigger.
 */
async function retryUploadBatch(paths: string[], parentID: string, encrypt: boolean): Promise<void> {
    await withTransferFlow(() => uploadPathsBatch(paths, parentID, encrypt));
}

/**
 * Runs an upload selection.
 *
 * One file keeps a row of its own -- the file *is* the transfer, and an
 * aggregate over one thing says nothing extra. More than one gets a single
 * aggregate row with the files currently moving listed under it, so the reader
 * can see both how far the batch has got and what it is working on.
 */
async function uploadPathsBatch(paths: string[], parentID: string, encrypt: boolean) {
    if (activeTransferDriveId === null) activeTransferDriveId = state.activeChannel?.id ?? null;
    setTransferDirectionActive('upload', true);
    state.uploadBatch = { total: paths.length, done: 0, failed: 0 };

    const aggregated = paths.length > 1;
    const nextTransfers = new Map();
    if (aggregated) {
        uploadBatchFailureReasons.length = 0;
        uploadBatch = new TransferBatch(paths.length);
        pushTransferStart({
            id: UPLOAD_BATCH_TRANSFER_ID,
            direction: 'up',
            name: `Uploading ${paths.length} files`,
        });
        refreshUploadBatchRow();
    } else {
        // Per-file rows need somewhere to remember what each file is, both for
        // the row and for the safety sweep below. The aggregate keeps its own
        // bounded state instead, so a batch of any size retains none of this.
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
            // An error the user can only read is a dead end: the whole reason
            // they are looking at it is that they still want the files up. The
            // retry re-runs the same batch with the same destination, so the
            // recovery is one tap rather than re-finding the files in a picker.
            const toastId = `upload-retry:${Date.now()}`;
            notify({
                id: toastId,
                level: 'error',
                title: 'Upload failed',
                body: humanizeBackendError(error),
                action: {
                    label: 'Retry',
                    // One shot, and gone from the screen the moment it is
                    // taken. The toast is sticky, so a second tap would start a
                    // second batch over the first: the backend keeps one cancel
                    // handle per direction, so beginning again cancels the
                    // retry already running, and the fresh uploadBatch counters
                    // would be counting the wrong files.
                    run: oneShot(() => {
                        dismissNotification(toastId);
                        void retryUploadBatch(paths, parentID, encrypt);
                    }),
                },
            });
        }
    } finally {
        setTransferDirectionActive('upload', false);
        // Safety sweep: by the time UploadToDriveFS resolves, every upload in
        // the batch has terminated on the backend. If a Wails event was dropped,
        // an entry may still be stuck 'active' at 100% in the bell.
        // markTransferDone is idempotent against terminal entries.
        if (uploadBatch) {
            finalizeUploadBatch(uploadThrew);
        } else {
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
        }
        invalidateTransferCaches();
        state.uploadBatch = null;
        state.cancelingUpload = false;
    }
}



export function chooseFilesForCurrentFolder(): void {
    void uploadWithParentID(state.currentFolderId);
}

export function chooseFolderForCurrentFolder(): void {
    void importFolderWithParentID(state.currentFolderId);
}

function activateFileDropEvents(): void {
    // The drop target itself is opted in via the `data-file-drop-target`
    // attribute on #file-list (AppShell.svelte); Go delivers native drops as
    // the ordinary files_dropped event below.
    subscribeTransferEvent('files_dropped', (payload) => {
        // If an in-app drag-to-move is underway, ignore native drops entirely
        // (macOS can still fire one for the internal drag).
        if (state.dragState) return;
        const event = asObjectRecord(payload);
        const rawPaths = Array.isArray(payload) ? payload : event.paths;
        if (!Array.isArray(rawPaths) || !rawPaths.length) return;
        const paths = rawPaths.filter((path): path is string => typeof path === 'string');
        // Photos and the trash are not folders: a drop there used to start an
        // upload into whatever folder the drive had last shown, behind the
        // trash's chrome.
        if (!paths.length || !state.activeChannel || state.virtualView !== null) return;
        const x = Number(event.x);
        const y = Number(event.y);
        if (!Number.isFinite(x) || !Number.isFinite(y)) return;
        const target = document.elementFromPoint(x, y);
        if (!target || !(target as HTMLElement).closest('#file-list')) return;
        void withTransferFlow(() => importSelection(state.currentFolderId, paths));
    });
}

export function activateTransferSurfaces(): () => void {
    teardownTransferSurfaces();
    activateDownloadProgressEvents();
    activateUploadProgressEvents();
    activateFileDropEvents();
    // Makes the phone's copy-out-of-the-picker wait visible; silent everywhere
    // the host hands back paths directly. Torn down with the rest.
    transferUnsubscribers.push(activateFileSelectionProgress());
    // The log outlives the app. The queue above it is deliberately not stored
    // alongside: every job in it already has a bell row whose key carries the
    // drive, the kind and the message id, which is everything enqueueDownload
    // needs, and a second copy of that would only be a second thing to
    // disagree. A job interrupted by a kill comes back as a failed row, and the
    // Retry that key already powers is what puts it back in this queue.
    stopTransferPersistence = activateTransferPersistence();
    return teardownTransferSurfaces;
}

export function teardownTransferSurfaces(): void {
    const unsubscribers = transferUnsubscribers;
    transferUnsubscribers = [];
    for (const unsubscribe of unsubscribers) unsubscribe();
    const stopPersistence = stopTransferPersistence;
    stopTransferPersistence = null;
    stopPersistence?.();
}
