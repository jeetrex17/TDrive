// Rename modal for TDrive frontend

import { addTelegramFileToDrive, renameFile, renameFolder } from '../../api';
import { invalidateFolderIndex } from '../../state';
import { callWithPasswordRetry } from './encryption-password';
import { humanizeBackendError } from '../errors';
import { appActions } from '../app-actions';
import {
    closeRenameModalView,
    openRenameModalView,
    setRenameModalError,
    setRenameModalInFlight,
    type RenameModalTarget,
} from '../../ui/modals/rename-modal-store';
import type { FileCommandItem } from '../../ui/file-list/types';

async function ensureFileInTdriveSystem(target: RenameModalTarget): Promise<void> {
    if (target.type !== 'file' || target.source !== 'tg') return;

    const result = await addTelegramFileToDrive(
        target.id,
        target.name,
        target.size,
        target.parentId,
    );

    if (!result.ok) throw new Error(humanizeBackendError(result.error));
}


export function openRenameModal(target: FileCommandItem): void {
    openRenameModalView(target);
}

export async function submitRename(target: RenameModalTarget, rawName: string): Promise<void> {
    const nextName = (rawName || '').trim();
    if (!nextName) {
        setRenameModalError("Name can't be empty.");
        return;
    }
    if (/[\\/]/.test(nextName)) {
        setRenameModalError("Name can't include / or \\.");
        return;
    }

    setRenameModalError('');
    setRenameModalInFlight(true);
    try {
        let result;
        if (target.type === 'folder') {
            result = await callWithPasswordRetry(() => renameFolder(target.id, nextName));
        } else {
            await ensureFileInTdriveSystem(target);
            result = await callWithPasswordRetry(() => renameFile(target.id, nextName));
        }

        if (!result.ok) {
            setRenameModalError(humanizeBackendError(result.error));
            return;
        }
        invalidateFolderIndex();
        closeRenameModalView();
        appActions().refreshFiles();
    } catch (err) {
        setRenameModalError(humanizeBackendError(err));
    } finally {
        setRenameModalInFlight(false);
    }
}
