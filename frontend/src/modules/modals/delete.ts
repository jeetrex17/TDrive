// Delete modal for TDrive frontend

import { DeleteFile } from '../../../wailsjs/go/main/App';
import { clearSelection } from '../selection';
import { ensureNotInsideDeletedFolder } from '../navigation';
import { deleteFolder } from '../drive-data';
import { notify, dismissNotification } from '../notifications';
import { openEncryptionPasswordModal } from './encryption-password';
import DeleteModal from '../../ui/modals/DeleteModal.svelte';
import { closeDeleteModalView, openDeleteModalView } from '../../ui/modals/delete-modal-store';
import { mountSvelte, type SvelteMountHandle } from '../../ui/mount';

let deleteModalHandle: SvelteMountHandle<Record<string, unknown>> | null = null;

// The target the open modal would act on. Overwritten by every openDeleteModal
// call and cleared on confirm; a cancel can leave a stale value behind, which
// is harmless because nothing reads it while the modal is closed.
let pendingTarget: any = null;

function successTitle(item: any) {
    const name = String(item?.name || '').trim();
    if (!name) return item?.type === 'folder' ? 'Folder deleted' : 'File deleted';
    return item?.type === 'folder' ? `Deleted folder "${name}"` : `Deleted "${name}"`;
}

function failureTitle(item: any) {
    const name = String(item?.name || '').trim();
    if (!name) return item?.type === 'folder' ? 'Could not delete folder' : 'Could not delete file';
    return item?.type === 'folder' ? `Could not delete folder "${name}"` : `Could not delete "${name}"`;
}

async function deleteFileWithPasswordRetry(id: any) {
    let res = await DeleteFile(Number(id));
    if (typeof res === "string" && res.startsWith("Error") && /encryption password required/i.test(res)) {
        const ok = await openEncryptionPasswordModal();
        if (!ok) return "Error: Encryption password required";
        res = await DeleteFile(Number(id));
    }
    return res;
}

// Folder delete also rejects with "encryption password required" when the
// subtree contains encrypted files and the vault is locked. Mirror the file
// path: prompt for the password once and retry.
async function deleteFolderWithPasswordRetry(id: any) {
    let res = await deleteFolder(String(id));
    if (typeof res === "string" && res.startsWith("Error") && /encryption password required/i.test(res)) {
        const ok = await openEncryptionPasswordModal();
        if (!ok) return "Error: Encryption password required";
        res = await deleteFolder(String(id));
    }
    return res;
}

export function openDeleteModal(target: any) {
    if (!target) return;

    pendingTarget = target;
    const name = (target?.name || "").trim();

    let title: string;
    let itemName = "";
    let subtitle: string;
    let confirmLabel: string;

    if (target?.type === "bulk") {
        const rawItems = Array.isArray(target?.items) ? target.items : [];

        // Pre-filter for the shared-drive owner-only rule. The backend
        // would reject these anyway, but filtering up front avoids the
        // confusing "some items were not deleted" toast for files the
        // user never had permission to delete in the first place.
        const allowed = rawItems.filter((i: any) => i?.canDelete !== false);
        const skipped = rawItems.length - allowed.length;
        target.items = allowed;

        const total = allowed.length;
        const folders = allowed.filter((i: any) => i?.type === "folder").length;
        const files = allowed.filter((i: any) => i?.type === "file").length;

        title = total === 1 ? "Delete 1 item?" : `Delete ${total} items?`;
        const skippedNote = skipped > 0
            ? ` ${skipped} item(s) you don't own will be skipped.`
            : "";
        if (folders > 0 && files > 0) {
            subtitle = `This will delete ${folders} folder(s), all files inside them, and ${files} selected file(s) from Telegram. This action can't be undone.${skippedNote}`;
        } else if (folders > 0) {
            subtitle = `This will delete ${folders} folder(s) and all files inside them from Telegram. This action can't be undone.${skippedNote}`;
        } else if (files > 0) {
            subtitle = `This will remove ${files} file(s) from your Telegram channel. The action can't be undone.${skippedNote}`;
        } else {
            subtitle = `Nothing in your selection can be deleted. ${skipped} item(s) you don't own were skipped.`;
        }
        confirmLabel = total === 0 ? "Close" : "Delete";
    } else if (target?.type === "folder") {
        title = "Delete folder?";
        itemName = name;
        subtitle = "This will delete the folder and every file inside it from Telegram. This action can't be undone.";
        confirmLabel = "Delete folder and files";
    } else {
        title = "Delete file?";
        itemName = name;
        subtitle = "This will remove the file from your Telegram channel. The action can't be undone.";
        confirmLabel = "Delete file";
    }

    openDeleteModalView({ title, itemName, subtitle, confirmLabel });
}

export function setupDeleteModal() {
    const modal = document.getElementById("delete-modal");
    if (!modal || deleteModalHandle) return;

    modal.replaceChildren();
    deleteModalHandle = mountSvelte(DeleteModal, {
        target: modal,
        props: {
            onConfirm: confirmDelete,
        },
    });
}

async function confirmDelete(): Promise<void> {
    const target = pendingTarget;
    pendingTarget = null;
    closeDeleteModalView();
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
            const folders = items.filter((i: any) => i?.type === "folder");
            const files = items.filter((i: any) => i?.type === "file");
            const succeeded: any[] = [];
            const failures: any[] = [];

            for (const folder of folders) {
                try {
                    const res = await deleteFolderWithPasswordRetry(folder.id);
                    if (typeof res === "string" && res.startsWith("Error")) {
                        failures.push({ item: folder, error: res.replace(/^Error:?\s*/i, '') });
                        continue;
                    }
                    ensureNotInsideDeletedFolder(String(folder.id));
                    succeeded.push(folder);
                } catch (err) {
                    console.error("Delete folder failed:", folder, err);
                    failures.push({ item: folder, error: (err as any)?.message || String(err) });
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
                    failures.push({ item: file, error: (err as any)?.message || String(err) });
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
                ? await deleteFolderWithPasswordRetry(target.id)
                : await deleteFileWithPasswordRetry(target.id);

            dismissNotification(progressId);
            if (typeof res === "string" && res.startsWith("Error")) {
                notify({
                    level: 'error',
                    title: failureTitle(target),
                    body: res.replace(/^Error:?\s*/i, ''),
                });
                // A failed delete can mean the backend already considers this
                // row gone (e.g. "File not found"). Refresh so a stale/ghost
                // row doesn't sit there re-clickable forever.
                window.refreshFiles();
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
    }
}
