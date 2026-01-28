// Rename modal for TDrive frontend

import { state } from '../../state.js';
import { RenameFile, RenameFolder, MsgToTdriveSystem } from '../../../wailsjs/go/main/App';

async function ensureFileInTdriveSystem(target) {
    if (!target || target.type !== "file") return;
    if (String(target.source || "fs") !== "tg") return;

    const res = await MsgToTdriveSystem(
        Number(target.id),
        String(target.name || ""),
        Number(target.size || 0),
        String(target.parentId || "")
    );

    if (typeof res === "string" && res.startsWith("Error")) {
        throw new Error(res);
    }
}

export function openRenameModal(target) {
    const modal = document.getElementById("rename-modal");
    const title = document.getElementById("rename-modal-title");
    const subtitle = document.getElementById("rename-modal-subtitle");
    const input = document.getElementById("rename-input");
    const errorEl = document.getElementById("rename-error");

    if (!modal || !title || !subtitle || !input) return;

    state.pendingRenameTarget = target;
    if (errorEl) {
        errorEl.innerText = "";
        errorEl.style.display = "none";
    }

    const isFolder = target?.type === "folder";
    title.textContent = isFolder ? "Rename folder" : "Rename file";
    subtitle.textContent = isFolder ? "Choose a new folder name." : "Choose a new file name.";

    input.value = String(target?.name || "");
    modal.style.display = "flex";

    requestAnimationFrame(() => {
        input.focus();
        const value = input.value || "";
        const dot = value.lastIndexOf(".");
        if (!isFolder && dot > 0 && dot < value.length - 1) {
            input.setSelectionRange(0, dot);
        } else {
            input.select();
        }
    });
}

export function setupRenameModal() {
    const modal = document.getElementById("rename-modal");
    const cancelBtn = document.getElementById("rename-cancel");
    const confirmBtn = document.getElementById("rename-confirm");
    const input = document.getElementById("rename-input");
    const errorEl = document.getElementById("rename-error");

    if (!modal || !cancelBtn || !confirmBtn || !input) return;

    const showError = (message) => {
        if (!errorEl) return;
        errorEl.innerText = message || "";
        errorEl.style.display = message ? "block" : "none";
    };

    const close = () => {
        showError("");
        modal.style.display = "none";
        state.pendingRenameTarget = null;
    };

    cancelBtn.addEventListener("click", close);
    modal.addEventListener("click", (e) => {
        if (e.target === modal) close();
    });

    input.addEventListener("keydown", (e) => {
        if (e.key === "Enter") {
            e.preventDefault();
            confirmBtn.click();
        }
        if (e.key === "Escape") {
            e.preventDefault();
            close();
        }
    });

    confirmBtn.addEventListener("click", async () => {
        if (!state.pendingRenameTarget) return;
        const nextName = (input.value || "").trim();
        if (!nextName) {
            showError("Name can't be empty.");
            return;
        }
        if (/[\\/]/.test(nextName)) {
            showError("Name can't include / or \\.");
            return;
        }

        showError("");

        try {
            let res = "";
            if (state.pendingRenameTarget.type === "folder") {
                res = await RenameFolder(String(state.pendingRenameTarget.id), nextName);
            } else {
                await ensureFileInTdriveSystem(state.pendingRenameTarget);
                res = await RenameFile(Number(state.pendingRenameTarget.id), nextName);
            }

            if (typeof res === "string" && res.startsWith("Error")) {
                showError(res);
                return;
            }
            close();
            window.refreshFiles();
        } catch (err) {
            showError(err?.message || String(err));
        }
    });
}
