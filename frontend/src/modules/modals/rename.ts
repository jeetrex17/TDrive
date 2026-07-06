// Rename modal for TDrive frontend

import { RenameFile, RenameFolder, MsgToTdriveSystem } from '../../../wailsjs/go/main/App';
import { callWithPasswordRetry } from './encryption-password';
import { humanizeBackendError } from '../errors';
import RenameModal from '../../ui/modals/RenameModal.svelte';
import {
    closeRenameModalView,
    openRenameModalView,
    setRenameModalError,
    setRenameModalInFlight,
    type RenameModalTarget,
} from '../../ui/modals/rename-modal-store';
import { mountSvelte, type SvelteMountHandle } from '../../ui/mount';

let renameModalHandle: SvelteMountHandle<Record<string, unknown>> | null = null;

async function ensureFileInTdriveSystem(target: RenameModalTarget): Promise<void> {
    if (target.type !== 'file') return;
    if (String(target.source || 'fs') !== 'tg') return;

    const res = await MsgToTdriveSystem(
        Number(target.id),
        String(target.name || ''),
        Number(target.size || 0),
        String(target.parentId || ''),
    );

    if (typeof res === 'string' && res.startsWith('Error')) {
        throw new Error(humanizeBackendError(res));
    }
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

export function openRenameModal(target: any) {
    if (!target) return;
    openRenameModalView({
        type: target.type === 'folder' ? 'folder' : 'file',
        id: target.id,
        name: String(target.name || ''),
        size: Number(target.size || 0),
        parentId: String(target.parentId || ''),
        source: String(target.source || 'fs'),
    });
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
        let res = '';
        if (target.type === 'folder') {
            res = await callWithPasswordRetry(() => RenameFolder(String(target.id), nextName));
        } else {
            await ensureFileInTdriveSystem(target);
            res = await callWithPasswordRetry(() => RenameFile(Number(target.id), nextName));
        }

        if (typeof res === 'string' && res.startsWith('Error')) {
            setRenameModalError(humanizeBackendError(res));
            return;
        }
        closeRenameModalView();
        window.refreshFiles();
    } catch (err) {
        setRenameModalError(humanizeBackendError(err));
    } finally {
        setRenameModalInFlight(false);
    }
}
