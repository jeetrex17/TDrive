// Delete modal for TDrive frontend

import { state } from '../../state.js';
import { DeleteFile } from '../../../wailsjs/go/main/App';
import { clearSelection } from '../selection.js';
import { ensureNotInsideDeletedFolder } from '../navigation.js';
import { deleteFolder } from '../file-list.js';
import { notify, dismissNotification } from '../notifications.js';

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
}

export function setupDeleteModal() {
    const modal = document.getElementById("delete-modal");
    const cancelBtn = document.getElementById("delete-cancel");
    const confirmBtn = document.getElementById("delete-confirm");

    if (!modal || !cancelBtn || !confirmBtn) return;

    const close = () => {
        state.pendingDeleteTarget = null;
        modal.style.display = "none";
    };

    cancelBtn.addEventListener("click", close);
    modal.addEventListener("click", (e) => {
        if (e.target === modal) close();
    });

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
                const failures = [];

                for (const folder of folders) {
                    try {
                        const res = await deleteFolder(String(folder.id));
                        if (typeof res === "string" && res.startsWith("Error")) {
                            failures.push(`${folder.name || folder.id}: ${res}`);
                            continue;
                        }
                        ensureNotInsideDeletedFolder(String(folder.id));
                    } catch (err) {
                        console.error("Delete folder failed:", folder, err);
                        failures.push(`${folder.name || folder.id}: ${err?.message || String(err)}`);
                    }
                }

                for (const file of files) {
                    try {
                        const res = await DeleteFile(Number(file.id));
                        if (typeof res === "string" && res.startsWith("Error")) {
                            failures.push(`${file.name || file.id}: ${res}`);
                        }
                    } catch (err) {
                        console.error("Delete file failed:", file, err);
                        failures.push(`${file.name || file.id}: ${err?.message || String(err)}`);
                    }
                }

                clearSelection();
                dismissNotification(progressId);
                const successCount = items.length - failures.length;
                if (failures.length === 0) {
                    notify({
                        level: 'success',
                        title: items.length === 1 ? 'Item deleted' : `Deleted ${items.length} items`,
                    });
                } else {
                    notify({
                        level: 'error',
                        title: successCount > 0
                            ? `Deleted ${successCount} of ${items.length} items`
                            : 'Could not delete items',
                        body: failures.slice(0, 5).join('\n') + (failures.length > 5 ? '\n…' : ''),
                    });
                }
                window.refreshFiles();
            } else {
                const res = target.type === "folder"
                    ? await deleteFolder(String(target.id))
                    : await DeleteFile(Number(target.id));

                dismissNotification(progressId);
                if (typeof res === "string" && res.startsWith("Error")) {
                    notify({
                        level: 'error',
                        title: target.type === 'folder' ? 'Could not delete folder' : 'Could not delete file',
                        body: res.replace(/^Error:?\s*/i, ''),
                    });
                    return;
                }
                if (target.type === "folder") ensureNotInsideDeletedFolder(String(target.id));
                notify({
                    level: 'success',
                    title: target.type === 'folder' ? 'Folder deleted' : 'File deleted',
                    body: target.name ? String(target.name) : '',
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
