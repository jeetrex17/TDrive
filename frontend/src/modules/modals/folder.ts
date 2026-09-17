// New folder modal for TDrive frontend

import { invalidateFolderIndex, state } from '../../state';
import { createFolder } from '../../api';
import { notify } from '../notifications';
import { humanizeBackendError } from '../errors';
import { appActions } from '../app-actions';
import {
    closeFolderModalView,
    openFolderModalView,
    setFolderModalInFlight,
} from '../../ui/modals/folder-modal-store';
let inFlight = false;


export async function submitFolder(name: string): Promise<void> {
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
    appActions().refreshFiles();

    try {
        await createFolder(trimmed, parentId);
        invalidateFolderIndex();
        closeFolderModalView();
    } catch (err) {
        notify({
            level: 'error',
            title: 'Could not create folder',
            body: humanizeBackendError(err),
        });
    } finally {
        // Drop the pending overlay regardless of outcome. The follow-up
        // refreshFiles either shows the new real row (success) or shows the
        // prior state (error).
        //
        // Success says nothing further. The ghost row has been standing in the
        // list saying "Creating…" since before the round-trip started, and it
        // has just turned into the real folder in front of the person who asked
        // for it; a notice on top of that is the app reading its own work back.
        state.pendingFolderOps.delete(tempId);
        inFlight = false;
        setFolderModalInFlight(false);
        appActions().refreshFiles();
    }
}

export function openNewFolderModal() {
    openFolderModalView();
}
