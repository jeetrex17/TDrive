// New folder modal for TDrive frontend

import { state } from '../../state.js';
import { createFolder } from '../file-list.js';

export function setupFolderModal() {
    const modal = document.getElementById("folder-modal");
    const cancelBtn = document.getElementById("folder-cancel");
    const createBtn = document.getElementById("folder-create");
    const nameInput = document.getElementById("new-folder-name");

    if (!modal || !cancelBtn || !createBtn || !nameInput) return;

    const close = () => {
        modal.style.display = "none";
        nameInput.value = "";
    };

    cancelBtn.addEventListener("click", close);
    modal.addEventListener("click", (e) => {
        if (e.target === modal) close();
    });

    const submit = async () => {
        const name = (nameInput.value || "").trim();
        if (!name) return;

        const status = document.getElementById("status-msg");
        if (status) status.innerText = "Creating folder...";

        try {
            await createFolder(name, state.currentFolderId);
            close();
            window.refreshFiles();
        } catch (err) {
            alert("Failed to create folder: " + err);
        } finally {
            if (status) status.innerText = "Ready";
        }
    };

    createBtn.addEventListener("click", submit);
    nameInput.addEventListener("keydown", (e) => {
        if (e.key === "Enter") submit();
        if (e.key === "Escape") close();
    });
}

export function openNewFolderModal() {
    const modal = document.getElementById("folder-modal");
    const nameInput = document.getElementById("new-folder-name");
    if (!modal || !nameInput) return;

    modal.style.display = "flex";
    setTimeout(() => nameInput.focus(), 0);
}
