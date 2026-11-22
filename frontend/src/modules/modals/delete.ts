// Move to Trash for TDrive frontend.
//
// Delete does not destroy anything: the backend moves the file or folder into
// the Trash (backend/services/file/mutations.go), where it stays restorable
// until the Trash is emptied or the retention sweep purges it. Because of that
// it runs without a confirmation, and a successful move offers a one-tap Undo
// that restores the same object by its trash id (`f:<msgId>` for a file, the
// folder's own `d:…` id for a folder).

import { deleteFile, deleteFolder, type OperationResult } from '../../api';
import { restoreFromTrash } from '../../api/trash';
import { invalidateFolderIndex, state } from '../../state';
import { clearSelection } from '../selection';
import { ensureNotInsideDeletedFolder } from '../navigation';
import { dismissNotification, notify } from '../notifications';
import { humanizeBackendError } from '../errors';
import { appActions } from '../app-actions';
import { callWithPasswordRetry } from './encryption-password';
import { markRowsBusy } from '../../ui/file-list/busy-rows';
import type {
    FileCommandItem,
    FileCommandTarget,
    FolderCommandItem,
} from '../../ui/file-list/types';
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

function deleteInChannel(channelId: number, operation: () => Promise<OperationResult>): Promise<OperationResult> {
    // Password retry may invoke this callback again after a modal interaction.
    // Recheck at each invocation because the backend uses the active drive.
    if (activeChannelId() !== channelId) {
        return Promise.resolve({
            ok: false,
            error: { code: 'canceled', message: 'The active drive changed before deletion' },
        });
    }
    return operation();
}

/** Moves items to the Trash straight away; the result toast carries Undo. */
export function moveToTrash(target: FileCommandTarget): Promise<void> {
    const activeId = activeChannelId();
    const channels = target.type === 'bulk'
        ? target.items.map((item) => item.channelId ?? activeId)
        : [target.channelId ?? activeId];
    const channelId = channels[0] ?? activeId;
    if (channelId !== activeId || channels.some((id) => id !== channelId)) {
        notify({ level: 'info', title: 'Switch back to that drive to delete this item' });
        return Promise.resolve();
    }
    if (target.type !== 'bulk') return runMoveToTrash(target, channelId, 0);

    const allowed = target.items.filter((item) => item.canDelete !== false);
    if (allowed.length === 0) {
        notify({ level: 'info', title: 'You can only delete items you uploaded' });
        return Promise.resolve();
    }
    return runMoveToTrash({ ...target, items: allowed }, channelId, target.items.length - allowed.length);
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
function notifyMovedToTrash(items: FileCommandItem[], channelId: number, skipped = 0): void {
    if (items.length === 0) return;
    const objectIds = items.map(trashObjectId);
    const title = items.length === 1 ? 'Moved to Trash' : `Moved ${items.length} items to Trash`;
    const toastId = notify({
        level: 'success',
        title,
        body: skipped > 0 ? `Skipped ${skipped} you didn't upload.` : '',
        action: {
            label: 'Undo',
            run: () => {
                dismissNotification(toastId);
                void undoDelete(objectIds, channelId);
            },
        },
    });
}

async function runMoveToTrash(target: FileCommandTarget, channelId: number, skipped: number): Promise<void> {
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
            let interrupted = false;

            for (const folder of folders) {
                if (activeChannelId() !== channelId) { interrupted = true; break; }
                try {
                    const result = await callWithPasswordRetry(() => deleteInChannel(channelId, () => deleteFolder(folder.id)));
                    if (!result.ok) {
                        failures.push({ item: folder, error: humanizeBackendError(result.error) });
                        continue;
                    }
                    if (activeChannelId() === channelId) ensureNotInsideDeletedFolder(folder.id);
                    succeeded.push(folder);
                } catch (error) {
                    console.error('Delete folder failed:', folder, error);
                    failures.push({ item: folder, error: humanizeBackendError(error) });
                }
            }

            for (const file of files) {
                if (interrupted) break;
                if (activeChannelId() !== channelId) { interrupted = true; break; }
                try {
                    const result = await callWithPasswordRetry(() => deleteInChannel(channelId, () => deleteFile(file.id)));
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

            if (interrupted || activeChannelId() !== channelId) {
                notify({ level: 'info', title: 'Switch back to that drive to finish deleting' });
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
                notifyMovedToTrash(succeeded, channelId, skipped);
            }
            appActions().refreshFiles();
            return;
        }

        const result = target.type === 'folder'
            ? await callWithPasswordRetry(() => deleteInChannel(channelId, () => deleteFolder(target.id)))
            : await callWithPasswordRetry(() => deleteInChannel(channelId, () => deleteFile(target.id)));

        if (!result.ok) {
            notify({
                level: 'error',
                title: failureTitle(target),
                body: humanizeBackendError(result.error),
            });
            appActions().refreshFiles();
            return;
        }
        if (target.type === 'folder' && activeChannelId() === channelId) ensureNotInsideDeletedFolder(target.id);
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
