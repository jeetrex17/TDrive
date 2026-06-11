// Delete modal for TDrive frontend

import { state } from '../../state.js';
import { DeleteFile } from '../../../wailsjs/go/main/App';
import { clearSelection } from '../selection.js';
import { ensureNotInsideDeletedFolder } from '../navigation.js';
import { deleteFolder } from '../drive-data.js';
import { notify, dismissNotification } from '../notifications.js';
import { openEncryptionPasswordModal } from './encryption-password.js';
import { installModalA11y } from './modal-a11y.js';

let a11y = null;

function successTitle(item) {
    const name = String(item?.name || '').trim();
    if (!name) return item?.type === 'folder' ? 'Folder deleted' : 'File deleted';
    return item?.type === 'folder' ? `Deleted folder "${name}"` : `Deleted "${name}"`;
}

function failureTitle(item) {
    const name = String(item?.name || '').trim();
    if (!name) return item?.type === 'folder' ? 'Could not delete folder' : 'Could not delete file';
    return item?.type === 'folder' ? `Could not delete folder "${name}"` : `Could not delete "${name}"`;
}

async function deleteFileWithPasswordRetry(id) {
    let res = await DeleteFile(Number(id));
    if (typeof res === "string" && res.startsWith("Error") && /encryption password required/i.test(res)) {
        const ok = await openEncryptionPasswordModal();
        if (!ok) return "Error: Encryption password required";
        res = await DeleteFile(Number(id));
    }
    return res;
}

export function openDeleteModal(target) {
    const modal = document.getElementById("delete-modal");
    const title = document.getElementById("delete-modal-title");
    const subtitle = document.getElementById("delete-modal-subtitle");
    const confirmBtn = document.getElementById("delete-confirm");

    if (!modal || !title || !subtitle || !confirmBtn) return;

    state.pendingDeleteTarget = target;

    const name = (target?.name || "").trim();

    if (target?.type === "bulk") {
        const rawItems = Array.isArray(target?.items) ? target.items : [];

        // Pre-filter for the shared-drive owner-only rule. The backend
        // would reject these anyway, but filtering up front avoids the
        // confusing "some items were not deleted" toast for files the
        // user never had permission to delete in the first place.
        const allowed = rawItems.filter((i) => i?.canDelete !== false);
        const skipped = rawItems.length - allowed.length;
        target.items = allowed;

        const total = allowed.length;
        const folders = allowed.filter((i) => i?.type === "folder").length;
        const files = allowed.filter((i) => i?.type === "file").length;

        title.textContent = total === 1 ? "Delete 1 item?" : `Delete ${total} items?`;
        const skippedNote = skipped > 0
            ? ` ${skipped} item(s) you don't own will be skipped.`
            : "";
        if (folders > 0 && files > 0) {
            subtitle.textContent = `This will hide ${folders} folder(s). Files inside those folders are not deleted. The ${files} selected file(s) will be removed from Telegram.${skippedNote}`;
        } else if (folders > 0) {
            subtitle.textContent = `This will hide ${folders} folder(s). Files inside become orphaned and are not deleted.${skippedNote}`;
        } else if (files > 0) {
            subtitle.textContent = `This will remove ${files} file(s) from your Telegram channel. The action can't be undone.${skippedNote}`;
        } else {
            subtitle.textContent = `Nothing in your selection can be deleted. ${skipped} item(s) you don't own were skipped.`;
        }
        confirmBtn.textContent = total === 0 ? "Close" : "Delete";
    } else if (target?.type === "folder") {
        title.textContent = name ? `Delete folder "${name}"?` : "Delete folder?";
        subtitle.textContent = "This hides the folder only. Files inside become orphaned and are not deleted.";
        confirmBtn.textContent = "Delete folder";
    } else {
        title.textContent = name ? `Delete "${name}"?` : "Delete file?";
        subtitle.textContent = "This will remove the file from your Telegram channel. The action can't be undone.";
        confirmBtn.textContent = "Delete file";
    }

    modal.style.display = "flex";
    a11y?.activate();
}

export function setupDeleteModal() {
    const modal = document.getElementById("delete-modal");
    const cancelBtn = document.getElementById("delete-cancel");
    const confirmBtn = document.getElementById("delete-confirm");

    if (!modal || !cancelBtn || !confirmBtn) return;

    const close = () => {
        a11y?.deactivate();
        state.pendingDeleteTarget = null;
        modal.style.display = "none";
    };

    cancelBtn.addEventListener("click", close);
    modal.addEventListener("click", (e) => {
        if (e.target === modal) close();
    });
    a11y = installModalA11y(modal, { requestClose: close, initialFocus: cancelBtn });

    confirmBtn.addEventListener("click", async () => {
        const target = state.pendingDeleteTarget;
        close();
        if (!target) return;

        const progressId = notify({
            id: 'deleting',
            level: 'info',
            title: 'Deleting…',
            sticky: true,
            spinner: true,
        });

        try {
            if (target.type === "bulk") {
                const items = Array.isArray(target.items) ? target.items : [];
                if (items.length === 0) {
                    dismissNotification(progressId);
                    return;
                }
                const folders = items.filter((i) => i?.type === "folder");
                const files = items.filter((i) => i?.type === "file");
                const succeeded = [];
                const failures = [];

                for (const folder of folders) {
                    try {
                        const res = await deleteFolder(String(folder.id));
                        if (typeof res === "string" && res.startsWith("Error")) {
                            failures.push({ item: folder, error: res.replace(/^Error:?\s*/i, '') });
                            continue;
                        }
                        ensureNotInsideDeletedFolder(String(folder.id));
                        succeeded.push(folder);
                    } catch (err) {
                        console.error("Delete folder failed:", folder, err);
                        failures.push({ item: folder, error: err?.message || String(err) });
                    }
                }

                for (const file of files) {
                    try {
                        const res = await deleteFileWithPasswordRetry(file.id);
                        if (typeof res === "string" && res.startsWith("Error")) {
                            failures.push({ item: file, error: res.replace(/^Error:?\s*/i, '') });
                            continue;
                        }
                        succeeded.push(file);
                    } catch (err) {
                        console.error("Delete file failed:", file, err);
                        failures.push({ item: file, error: err?.message || String(err) });
                    }
                }

                clearSelection();
                dismissNotification(progressId);
                for (const item of succeeded) {
                    notify({
                        level: 'success',
                        title: successTitle(item),
                    });
                }
                for (const { item, error } of failures) {
                    notify({
                        level: 'error',
                        title: failureTitle(item),
                        body: error,
                    });
                }
                window.refreshFiles();
            } else {
                const res = target.type === "folder"
                    ? await deleteFolder(String(target.id))
                    : await deleteFileWithPasswordRetry(target.id);

                dismissNotification(progressId);
                if (typeof res === "string" && res.startsWith("Error")) {
                    notify({
                        level: 'error',
                        title: failureTitle(target),
                        body: res.replace(/^Error:?\s*/i, ''),
                    });
                    return;
                }
                if (target.type === "folder") ensureNotInsideDeletedFolder(String(target.id));
                notify({
                    level: 'success',
                    title: successTitle(target),
                });
                window.refreshFiles();
            }
        } catch (err) {
            console.error("Delete failed:", err);
            dismissNotification(progressId);
            notify({
                level: 'error',
                title: 'Delete failed',
                body: 'Check the console for details.',
            });
        } finally {
            // No-op trailer; the legacy 2-second status reset is gone with
            // the status pill.
        }
    });
}
