// Delete modal for TDrive frontend

import { deleteFile, deleteFolder } from '../../api';
import { invalidateFolderIndex } from '../../state';
import { clearSelection } from '../selection';
import { ensureNotInsideDeletedFolder } from '../navigation';
import { notify, dismissNotification } from '../notifications';
import { humanizeBackendError } from '../errors';
import { appActions } from '../app-actions';
import { callWithPasswordRetry } from './encryption-password';
import { closeDeleteModalView, openDeleteModalView } from '../../ui/modals/delete-modal-store';
import type {
    FileCommandItem,
    FileCommandTarget,
    FolderCommandItem,
} from '../../ui/file-list/types';
let pendingTarget: FileCommandTarget | null = null;

function successTitle(item: FileCommandItem): string {
    const name = item.name.trim();
    if (!name) return item.type === 'folder' ? 'Folder deleted' : 'File deleted';
    return item.type === 'folder' ? `Deleted folder "${name}"` : `Deleted "${name}"`;
}

/**
 * One line for a whole batch. A selection of twelve used to answer with twelve
 * toasts into a stack that holds two on a phone, so eleven of them evicted each
 * other on the way past and the user read whichever happened to be last.
 */
function batchTitle(items: FileCommandItem[], verb: string): string {
    if (items.length === 1) return verb === 'Deleted' ? successTitle(items[0]) : failureTitle(items[0]);
    const folders = items.filter(isFolder).length;
    const files = items.length - folders;
    if (folders === 0) return `${verb} ${files} files`;
    if (files === 0) return `${verb} ${folders} folders`;
    return `${verb} ${items.length} items`;
}

function failureTitle(item: FileCommandItem): string {
    const name = item.name.trim();
    if (!name) return item.type === 'folder' ? 'Could not delete folder' : 'Could not delete file';
    return item.type === 'folder' ? `Could not delete folder "${name}"` : `Could not delete "${name}"`;
}

function isFolder(item: FileCommandItem): item is FolderCommandItem {
    return item.type === 'folder';
}


export function openDeleteModal(target: FileCommandTarget): void {
    pendingTarget = target;

    let title: string;
    let itemName = '';
    let subtitle: string;
    let confirmLabel: string;

    if (target.type === 'bulk') {
        const allowed = target.items.filter((item) => item.canDelete !== false);
        const skipped = target.items.length - allowed.length;
        pendingTarget = { ...target, items: allowed };

        const total = allowed.length;
        const folders = allowed.filter(isFolder).length;
        const files = total - folders;
        title = total === 1 ? 'Delete 1 item?' : `Delete ${total} items?`;
        const skippedNote = skipped > 0
            ? ` ${skipped} item(s) you don't own will be skipped.`
            : '';
        if (folders > 0 && files > 0) {
            subtitle = `This will delete ${folders} folder(s), all files inside them, and ${files} selected file(s) from Telegram. This action can't be undone.${skippedNote}`;
        } else if (folders > 0) {
            subtitle = `This will delete ${folders} folder(s) and all files inside them from Telegram. This action can't be undone.${skippedNote}`;
        } else if (files > 0) {
            subtitle = `This will remove ${files} file(s) from your Telegram channel. The action can't be undone.${skippedNote}`;
        } else {
            subtitle = `Nothing in your selection can be deleted. ${skipped} item(s) you don't own were skipped.`;
        }
        confirmLabel = total === 0 ? 'Close' : 'Delete';
    } else if (target.type === 'folder') {
        title = 'Delete folder?';
        itemName = target.name.trim();
        subtitle = "This will delete the folder and every file inside it from Telegram. This action can't be undone.";
        confirmLabel = 'Delete';
    } else {
        title = 'Delete file?';
        itemName = target.name.trim();
        subtitle = "This will remove the file from your Telegram channel. The action can't be undone.";
        confirmLabel = 'Delete';
    }

    openDeleteModalView({ title, itemName, subtitle, confirmLabel });
}


export async function confirmDelete(): Promise<void> {
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
        if (target.type === 'bulk') {
            if (target.items.length === 0) {
                dismissNotification(progressId);
                return;
            }
            const folders = target.items.filter(isFolder);
            const files = target.items.filter((item) => item.type === 'file');
            const succeeded: FileCommandItem[] = [];
            const failures: Array<{ item: FileCommandItem; error: string }> = [];

            for (const folder of folders) {
                try {
                    const result = await callWithPasswordRetry(() => deleteFolder(folder.id));
                    if (!result.ok) {
                        failures.push({ item: folder, error: humanizeBackendError(result.error) });
                        continue;
                    }
                    ensureNotInsideDeletedFolder(folder.id);
                    succeeded.push(folder);
                } catch (error) {
                    console.error('Delete folder failed:', folder, error);
                    failures.push({ item: folder, error: humanizeBackendError(error) });
                }
            }

            for (const file of files) {
                try {
                    const result = await callWithPasswordRetry(() => deleteFile(file.id));
                    if (!result.ok) {
                        failures.push({ item: file, error: humanizeBackendError(result.error) });
                        continue;
                    }
                    succeeded.push(file);
                } catch (error) {
                    console.error('Delete file failed:', file, error);
                    failures.push({ item: file, error: humanizeBackendError(error) });
                }
            }

            clearSelection();
            dismissNotification(progressId);
            if (succeeded.length > 0) {
                notify({ level: 'success', title: batchTitle(succeeded, 'Deleted') });
            }
            if (failures.length > 0) {
                // The reasons are worth keeping, but only the first is worth a
                // toast: past two or three they stop being read and start
                // being dismissed. The rest stay in the bell's history.
                const [first] = failures;
                notify({
                    level: 'error',
                    title: batchTitle(failures.map((entry) => entry.item), 'Could not delete'),
                    body: failures.length === 1
                        ? first.error
                        : `${first.error} Open Transfers for the rest.`,
                });
            }
            if (succeeded.length > 0) invalidateFolderIndex();
            appActions().refreshFiles();
            return;
        }

        const result = target.type === 'folder'
            ? await callWithPasswordRetry(() => deleteFolder(target.id))
            : await callWithPasswordRetry(() => deleteFile(target.id));

        dismissNotification(progressId);
        if (!result.ok) {
            notify({
                level: 'error',
                title: failureTitle(target),
                body: humanizeBackendError(result.error),
            });
            appActions().refreshFiles();
            return;
        }
        if (target.type === 'folder') ensureNotInsideDeletedFolder(target.id);
        notify({ level: 'success', title: successTitle(target) });
        invalidateFolderIndex();
        appActions().refreshFiles();
    } catch (error) {
        console.error('Delete failed:', error);
        dismissNotification(progressId);
        notify({
            level: 'error',
            title: 'Delete failed',
            body: humanizeBackendError(error),
        });
    }
}
