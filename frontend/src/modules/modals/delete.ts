// Delete modal for TDrive frontend.
//
// Delete does not destroy anything: the backend moves the file or folder into
// the Trash (backend/services/file/mutations.go), where it stays restorable
// until the Trash is emptied or the retention sweep purges it. The dialog says
// exactly that, and a successful delete offers a one-tap Undo that restores the
// same object by its trash id (`f:<msgId>` for a file, the folder's own `d:…`
// id for a folder).

import { deleteFile, deleteFolder } from '../../api';
import { restoreFromTrash } from '../../api/trash';
import { invalidateFolderIndex, state } from '../../state';
import { clearSelection } from '../selection';
import { ensureNotInsideDeletedFolder } from '../navigation';
import { dismissNotification, notify } from '../notifications';
import { humanizeBackendError } from '../errors';
import { appActions } from '../app-actions';
import { callWithPasswordRetry } from './encryption-password';
import { closeDeleteModalView, openDeleteModalView } from '../../ui/modals/delete-modal-store';
import { markRowsBusy } from '../../ui/file-list/busy-rows';
import type {
    FileCommandItem,
    FileCommandTarget,
    FolderCommandItem,
} from '../../ui/file-list/types';
let pendingTarget: FileCommandTarget | null = null;

/**
 * One line for a whole batch. A selection of twelve used to answer with twelve
 * toasts into a stack that holds two on a phone, so eleven of them evicted each
 * other on the way past and the user read whichever happened to be last.
 */
function failureBatchTitle(items: FileCommandItem[]): string {
    if (items.length === 1) return failureTitle(items[0]);
    const folders = items.filter(isFolder).length;
    const files = items.length - folders;
    if (folders === 0) return `Could not delete ${files} files`;
    if (files === 0) return `Could not delete ${folders} folders`;
    return `Could not delete ${items.length} items`;
}

function failureTitle(item: FileCommandItem): string {
    const name = item.name.trim();
    if (!name) return item.type === 'folder' ? 'Could not delete folder' : 'Could not delete file';
    return item.type === 'folder' ? `Could not delete folder "${name}"` : `Could not delete "${name}"`;
}

function isFolder(item: FileCommandItem): item is FolderCommandItem {
    return item.type === 'folder';
}

/** The trash handle for an item: a folder carries its own `d:…` id, a file is addressed by message id. */
function trashObjectId(item: FileCommandItem): string {
    return item.type === 'folder' ? item.id : `f:${item.id}`;
}

function activeChannelId(): number {
    return Number(state.activeChannel?.id ?? 0);
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
        title = total === 1 ? 'Move 1 item to Trash?' : `Move ${total} items to Trash?`;
        const skippedNote = skipped > 0
            ? ` ${skipped} item(s) you don't own will be skipped.`
            : '';
        if (total === 0) {
            subtitle = `Nothing in your selection can be deleted. ${skipped} item(s) you don't own were skipped.`;
        } else if (folders > 0 && files > 0) {
            subtitle = `${folders} folder(s) with everything inside them and ${files} file(s) move to the Trash. You can restore them from the Trash until they are purged.${skippedNote}`;
        } else if (folders > 0) {
            subtitle = `${folders} folder(s) and everything inside them move to the Trash. You can restore them from the Trash until they are purged.${skippedNote}`;
        } else {
            subtitle = `${files} file(s) move to the Trash. You can restore them from the Trash until they are purged.${skippedNote}`;
        }
        confirmLabel = total === 0 ? 'Close' : 'Move to Trash';
    } else if (target.type === 'folder') {
        title = 'Move folder to Trash?';
        itemName = target.name.trim();
        subtitle = 'The folder and everything inside it moves to the Trash. You can restore it from the Trash until it is purged.';
        confirmLabel = 'Move to Trash';
    } else {
        title = 'Move file to Trash?';
        itemName = target.name.trim();
        subtitle = 'It moves to the Trash. You can restore it from the Trash until it is purged.';
        confirmLabel = 'Move to Trash';
    }

    openDeleteModalView({ title, itemName, subtitle, confirmLabel });
}

/**
 * Restores the just-trashed items, if the drive is still the one they were
 * deleted from. A trash id is unique only within its channel, so a restore
 * after a drive switch could land on a different object that happens to share
 * the id; rather than risk that, Undo declines once the active drive changes.
 */
async function undoDelete(objectIds: string[], channelId: number): Promise<void> {
    if (activeChannelId() !== channelId) {
        notify({ level: 'info', title: 'Switch back to that drive to restore from the Trash' });
        return;
    }
    let restored = 0;
    const reasons: string[] = [];
    let switchedDrive = false;
    for (const objectId of objectIds) {
        if (activeChannelId() !== channelId) {
            switchedDrive = true;
            break;
        }
        try {
            const result = await restoreFromTrash(channelId, objectId);
            if (result.ok) restored += 1;
            else reasons.push(humanizeBackendError(result.error));
        } catch (error) {
            reasons.push(humanizeBackendError(error));
        }
    }
    if (restored > 0) {
        invalidateFolderIndex();
        if (activeChannelId() === channelId) appActions().refreshFiles();
    }
    if (switchedDrive) {
        notify({ level: 'info', title: 'Switch back to that drive to restore from the Trash' });
    }
    if (reasons.length > 0) {
        notify({ level: 'error', title: 'Could not restore from the Trash', body: reasons[0] });
    }
}

/** Confirms the move to Trash, with a one-tap Undo on the items that made it. */
function notifyMovedToTrash(items: FileCommandItem[], channelId: number): void {
    if (items.length === 0) return;
    const objectIds = items.map(trashObjectId);
    const title = items.length === 1 ? 'Moved to Trash' : `Moved ${items.length} items to Trash`;
    const toastId = notify({
        level: 'success',
        title,
        action: {
            label: 'Undo',
            run: () => {
                dismissNotification(toastId);
                void undoDelete(objectIds, channelId);
            },
        },
    });
}

export async function confirmDelete(): Promise<void> {
    const target = pendingTarget;
    pendingTarget = null;
    closeDeleteModalView();
    if (!target) return;

    // The channel the delete runs against; Undo must not restore into another.
    const channelId = activeChannelId();

    // The rows being deleted go quiet for the duration and then disappear.
    // That is the report; a notice about rows the user is already looking at,
    // parked over the list, was telling them something the list should say.
    // Rows carry their id as a string; a Telegram file's is a number here.
    const releaseBusy = markRowsBusy(
        (target.type === 'bulk' ? target.items : [target]).map((item) => String(item.id)),
    );

    try {
        if (target.type === 'bulk') {
            if (target.items.length === 0) return;
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
            if (failures.length > 0) {
                // The reasons are worth keeping, but only the first is worth a
                // toast: past two or three they stop being read and start
                // being dismissed. The rest stay in the bell's history.
                const [first] = failures;
                notify({
                    level: 'error',
                    title: failureBatchTitle(failures.map((entry) => entry.item)),
                    body: failures.length === 1
                        ? first.error
                        : `${first.error} Open Transfers for the rest.`,
                });
            }
            if (succeeded.length > 0) {
                invalidateFolderIndex();
                notifyMovedToTrash(succeeded, channelId);
            }
            appActions().refreshFiles();
            return;
        }

        const result = target.type === 'folder'
            ? await callWithPasswordRetry(() => deleteFolder(target.id))
            : await callWithPasswordRetry(() => deleteFile(target.id));

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
        invalidateFolderIndex();
        notifyMovedToTrash([target], channelId);
        appActions().refreshFiles();
    } catch (error) {
        console.error('Delete failed:', error);
        notify({
            level: 'error',
            title: 'Delete failed',
            body: humanizeBackendError(error),
        });
    } finally {
        // A row left marked is a row the user can no longer touch.
        releaseBusy();
    }
}
