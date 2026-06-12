// Upload/Download progress handling for TDrive frontend.
//
// All transfer state surfaces through the notification bell — there is no
// separate transfer pill or sheet anymore. Active transfers feed the bell's
// hover popover via pushTransferStart/updateTransferProgress/markTransferDone.
// Completed transfers stay in the bell's "Recent" panel until cleared.

import { state } from '../state';
import { SelectFiles, DownloadFile } from '../../wailsjs/go/main/App';
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

const DOWNLOAD_TERMINAL_STATES = new Set(["done", "failed", "canceled"]);

function isDownloadTerminalState(status: any) {
    return DOWNLOAD_TERMINAL_STATES.has(String(status || ""));
}

function normalizeDownloadResult(result: any) {
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

export function setupDownloadProgress() {
    if (!window.runtime?.EventsOn) return;

    window.runtime.EventsOn("download_progress", (percent: any) => {
        const activeId = state.activeDownloadId;
        if (activeId === null || activeId === undefined) return;
        const value = Number(percent);
        if (!Number.isFinite(value)) return;

        const clamped = Math.max(0, Math.min(100, value));
        const item = (state.downloadQueue || []).find((entry) => entry.id === activeId);
        if (!item) return;

        item.progress = clamped;
        if (!isDownloadTerminalState(item.state)) {
            item.state = "downloading";
        }
        updateTransferProgress({ id: item.id, direction: 'down', progress: clamped });
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
        let result = normalizeDownloadResult(await DownloadFile(Number(next.id), Number(next.id)));
        // If the backend needs the encryption password, prompt once and
        // retry. This avoids a separate per-file encryption lookup before
        // download starts.
        if (result.status === "error" && /encryption password required/i.test(result.message || "")) {
            const ok = await openEncryptionPasswordModal();
            if (ok) {
                result = normalizeDownloadResult(await DownloadFile(Number(next.id), Number(next.id)));
            }
        }
        next.message = result.message;

        if (result.status === "success") {
            next.state = "done";
            next.progress = 100;
            markTransferDone({ id: next.id, direction: 'down', status: 'done' });
        } else if (result.status === "canceled") {
            next.state = "canceled";
            markTransferDone({ id: next.id, direction: 'down', status: 'canceled' });
        } else {
            const canceled = state.cancelingDownload;
            next.state = canceled ? "canceled" : "failed";
            markTransferDone({ id: next.id, direction: 'down', status: canceled ? 'canceled' : 'failed' });
        }
    } catch (err) {
        console.error("Download failed:", err);
        next.message = "Download failed";
        const canceled = state.cancelingDownload;
        next.state = canceled ? "canceled" : "failed";
        markTransferDone({ id: next.id, direction: 'down', status: canceled ? 'canceled' : 'failed' });
    } finally {
        state.cancelingDownload = false;
        state.activeDownloadId = null;
        startNextDownload();
    }
}

export function enqueueDownload(id: any, name: any, size: any) {
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

    window.runtime.EventsOn("upload_error", (id: any, name: any) => {
        const uploadId = Number(id);
        if (!Number.isFinite(uploadId)) return;
        if (state.importBatch) {
            importProgressById.delete(uploadId);
            importFileIds.delete(uploadId);
            state.importBatch.failed += 1;
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

        if (batchFinished) {
            window.refreshFiles();
        }
    });

    window.runtime.EventsOn("import_start", () => {
        importProgressById.clear();
        importFileIds.clear();
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
            notify({
                level: failedUploads > 0 ? 'error' : 'info',
                title: `Imported ${uploaded === 1 ? '1 file' : `${uploaded} files`}`,
                body: bits.join('  ·  '),
            });
        }
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
        notify({ level: 'error', title: 'Upload failed', body: String(err) });
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
    const btn = document.getElementById('upload-btn');
    const menu = document.getElementById('upload-menu');
    if (!btn || !menu) return;
    const filesItem = document.getElementById('upload-menu-files');
    const folderItem = document.getElementById('upload-menu-folder');

    const close = (returnFocus = false) => {
        menu.style.display = 'none';
        btn.setAttribute('aria-expanded', 'false');
        if (returnFocus) (btn as HTMLElement).focus();
    };
    const open = () => {
        menu.style.display = 'flex';
        btn.setAttribute('aria-expanded', 'true');
        (filesItem as HTMLElement | null)?.focus();
    };

    btn.addEventListener('click', (e) => {
        e.stopPropagation();
        if (menu.style.display === 'none') open(); else close();
    });
    document.addEventListener('click', (e) => {
        if (menu.style.display !== 'none' && e.target !== btn && !menu.contains(e.target as Node)) close();
    });
    document.addEventListener('keydown', (e) => {
        if (e.key === 'Escape' && menu.style.display !== 'none') close(true);
    });
    // Arrow-key navigation between the menu items.
    menu.addEventListener('keydown', (e) => {
        if (e.key !== 'ArrowDown' && e.key !== 'ArrowUp') return;
        e.preventDefault();
        const items = [filesItem, folderItem].filter(Boolean) as HTMLElement[];
        if (!items.length) return;
        const idx = items.indexOf(document.activeElement as HTMLElement);
        const next = e.key === 'ArrowDown' ? (idx + 1) % items.length : (idx - 1 + items.length) % items.length;
        items[next].focus();
    });

    filesItem?.addEventListener('click', () => { close(); void uploadWithParentID(state.currentFolderId); });
    folderItem?.addEventListener('click', () => { close(); void importFolderWithParentID(state.currentFolderId); });
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
