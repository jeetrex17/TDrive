// New folder modal for TDrive frontend

import { state } from '../../state';
import { createFolder } from '../drive-data';
import { notify } from '../notifications';
import FolderModal from '../../ui/modals/FolderModal.svelte';
import {
    closeFolderModalView,
    openFolderModalView,
    setFolderModalInFlight,
} from '../../ui/modals/folder-modal-store';
import { mountSvelte, type SvelteMountHandle } from '../../ui/mount';

let folderModalHandle: SvelteMountHandle<Record<string, unknown>> | null = null;
let inFlight = false;

export function setupFolderModal() {
    const modal = document.getElementById("folder-modal");
    if (!modal || folderModalHandle) return;

    modal.replaceChildren();
    folderModalHandle = mountSvelte(FolderModal, {
        target: modal,
        props: {
            onSubmit: submitFolder,
        },
    });
}

async function submitFolder(name: string): Promise<void> {
    if (inFlight) return;

    const trimmed = name.trim();
    if (!trimmed) return;

    const parentId = state.currentFolderId;
    const tempId = `pending:${Date.now()}:${Math.random().toString(36).slice(2, 8)}`;

    // Register the pending op so refreshFiles can render a ghost row.
    state.pendingFolderOps.set(tempId, { parentId, name: trimmed });
    inFlight = true;
    setFolderModalInFlight(true);

    // Render immediately so the new row appears under the cursor before the
    // Telegram round-trip completes.
    window.refreshFiles();

    let failed = false;
    try {
        await createFolder(trimmed, parentId);
        closeFolderModalView();
    } catch (err) {
        failed = true;
        notify({
            level: 'error',
            title: 'Could not create folder',
            body: String(err),
        });
    } finally {
        // Drop the pending overlay regardless of outcome. The follow-up
        // refreshFiles either shows the new real row (success) or shows the
        // prior state (error).
        state.pendingFolderOps.delete(tempId);
        inFlight = false;
        setFolderModalInFlight(false);
        window.refreshFiles();
        if (!failed) {
            notify({ level: 'success', title: `Folder "${trimmed}" created` });
        }
    }
}

export function openNewFolderModal() {
    openFolderModalView();
}
