// Rename modal for TDrive frontend

import { addTelegramFileToDrive, renameFile, renameFolder } from '../../api';
import { callWithPasswordRetry } from './encryption-password';
import { humanizeBackendError } from '../errors';
import { appActions } from '../app-actions';
import RenameModal from '../../ui/modals/RenameModal.svelte';
import {
    closeRenameModalView,
    openRenameModalView,
    setRenameModalError,
    setRenameModalInFlight,
    type RenameModalTarget,
} from '../../ui/modals/rename-modal-store';
import type { FileCommandItem } from '../../ui/file-list/types';
import { mountSvelte, type SvelteMountHandle } from '../../ui/mount';

let renameModalHandle: SvelteMountHandle<Record<string, unknown>> | null = null;

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

export function setupRenameModal() {
    const modal = document.getElementById('rename-modal');
    if (!modal || renameModalHandle) return;

    modal.replaceChildren();
    renameModalHandle = mountSvelte(RenameModal, {
        target: modal,
        props: {
            onSubmit: submitRename,
        },
    });
}

export function openRenameModal(target: FileCommandItem): void {
    openRenameModalView(target);
}

async function submitRename(target: RenameModalTarget, rawName: string): Promise<void> {
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
        closeRenameModalView();
        appActions().refreshFiles();
    } catch (err) {
        setRenameModalError(humanizeBackendError(err));
    } finally {
        setRenameModalInFlight(false);
    }
}
