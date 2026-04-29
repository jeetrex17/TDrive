// New folder modal for TDrive frontend

import { state } from '../../state.js';
import { createFolder } from '../file-list.js';
import { notify, dismissNotification } from '../notifications.js';

export function setupFolderModal() {
    const modal = document.getElementById("folder-modal");
    const cancelBtn = document.getElementById("folder-cancel");
    const createBtn = document.getElementById("folder-create");
    const nameInput = document.getElementById("new-folder-name");

    if (!modal || !cancelBtn || !createBtn || !nameInput) return;

    let inFlight = false;

    const close = () => {
        modal.style.display = "none";
        nameInput.value = "";
    };

    cancelBtn.addEventListener("click", () => {
        if (inFlight) return;
        close();
    });
    modal.addEventListener("click", (e) => {
        if (e.target === modal && !inFlight) close();
    });

    const submit = async () => {
        if (inFlight) return; // guard against double-submit
        const name = (nameInput.value || "").trim();
        if (!name) return;

        const parentId = state.currentFolderId;
        const tempId = `pending:${Date.now()}:${Math.random().toString(36).slice(2, 8)}`;

        // Register the pending op so refreshFiles can render a ghost row.
        state.pendingFolderOps.set(tempId, { parentId, name });
        inFlight = true;
        createBtn.disabled = true;
        cancelBtn.disabled = true;

        // Render immediately so the new row appears under the cursor
        // before the Telegram round-trip completes.
        window.refreshFiles();

        let failed = false;
        try {
            await createFolder(name, parentId);
            close();
        } catch (err) {
            failed = true;
            notify({
                level: 'error',
                title: 'Could not create folder',
                body: String(err),
            });
        } finally {
            // Drop the pending overlay regardless of outcome. The follow-up
            // refreshFiles either shows the new real row (success) or
            // shows the prior state (error).
            state.pendingFolderOps.delete(tempId);
            inFlight = false;
            createBtn.disabled = false;
            cancelBtn.disabled = false;
            window.refreshFiles();
            if (!failed) {
                notify({ level: 'success', title: `Folder "${name}" created` });
            }
        }
    };

    createBtn.addEventListener("click", submit);
    nameInput.addEventListener("keydown", (e) => {
        if (e.key === "Enter") submit();
        if (e.key === "Escape" && !inFlight) close();
    });
}

export function openNewFolderModal() {
    const modal = document.getElementById("folder-modal");
    const nameInput = document.getElementById("new-folder-name");
    if (!modal || !nameInput) return;

    modal.style.display = "flex";
    setTimeout(() => nameInput.focus(), 0);
}
