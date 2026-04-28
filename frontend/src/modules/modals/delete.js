// Delete modal for TDrive frontend

import { state } from '../../state.js';
import { DeleteFile } from '../../../wailsjs/go/main/App';
import { clearSelection } from '../selection.js';
import { ensureNotInsideDeletedFolder } from '../navigation.js';
import { deleteFolder } from '../file-list.js';

export function openDeleteModal(target) {
    const modal = document.getElementById("delete-modal");
    const title = document.getElementById("delete-modal-title");
    const subtitle = document.getElementById("delete-modal-subtitle");
    const confirmBtn = document.getElementById("delete-confirm");

    if (!modal || !title || !subtitle || !confirmBtn) return;

    state.pendingDeleteTarget = target;

    const name = (target?.name || "").trim();

    if (target?.type === "bulk") {
        const items = Array.isArray(target?.items) ? target.items : [];
        const total = items.length;
        const folders = items.filter((i) => i?.type === "folder").length;
        const files = items.filter((i) => i?.type === "file").length;

        title.textContent = total === 1 ? "Delete 1 item?" : `Delete ${total} items?`;
        if (folders > 0 && files > 0) {
            subtitle.textContent = `This will hide ${folders} folder(s). Files inside those folders are not deleted. The ${files} selected file(s) will be removed from Telegram.`;
        } else if (folders > 0) {
            subtitle.textContent = `This will hide ${folders} folder(s). Files inside become orphaned and are not deleted.`;
        } else {
            subtitle.textContent = `This will remove ${files} file(s) from your Telegram channel. The action can't be undone.`;
        }
        confirmBtn.textContent = "Delete";
    } else if (target?.type === "folder") {
        title.textContent = name ? `Delete folder "${name}"?` : "Delete folder?";
        subtitle.textContent = "This hides the folder only. Files inside become orphaned and are not deleted.";
        confirmBtn.textContent = "Delete folder";
    } else {
        title.textContent = name ? `Delete "${name}"?` : "Delete file?";
        subtitle.textContent = "This will remove the file from your Telegram channel. The action can't be undone.";
        confirmBtn.textContent = "Delete file";
    }

    modal.style.display = "flex";
}

export function setupDeleteModal() {
    const modal = document.getElementById("delete-modal");
    const cancelBtn = document.getElementById("delete-cancel");
    const confirmBtn = document.getElementById("delete-confirm");

    if (!modal || !cancelBtn || !confirmBtn) return;

    const close = () => {
        state.pendingDeleteTarget = null;
        modal.style.display = "none";
    };

    cancelBtn.addEventListener("click", close);
    modal.addEventListener("click", (e) => {
        if (e.target === modal) close();
    });

    confirmBtn.addEventListener("click", async () => {
        const target = state.pendingDeleteTarget;
        close();
        if (!target) return;

        const status = document.getElementById("status-msg");
        if (status) status.innerText = "Deleting...";

        try {
            if (target.type === "bulk") {
                const items = Array.isArray(target.items) ? target.items : [];
                const folders = items.filter((i) => i?.type === "folder");
                const files = items.filter((i) => i?.type === "file");
                const failures = [];

                for (const folder of folders) {
                    try {
                        const res = await deleteFolder(String(folder.id));
                        if (typeof res === "string" && res.startsWith("Error")) {
                            failures.push(`${folder.name || folder.id}: ${res}`);
                            continue;
                        }
                        ensureNotInsideDeletedFolder(String(folder.id));
                    } catch (err) {
                        console.error("Delete folder failed:", folder, err);
                        failures.push(`${folder.name || folder.id}: ${err?.message || String(err)}`);
                    }
                }

                for (const file of files) {
                    try {
                        const res = await DeleteFile(Number(file.id));
                        if (typeof res === "string" && res.startsWith("Error")) {
                            failures.push(`${file.name || file.id}: ${res}`);
                        }
                    } catch (err) {
                        console.error("Delete file failed:", file, err);
                        failures.push(`${file.name || file.id}: ${err?.message || String(err)}`);
                    }
                }

                clearSelection();
                if (failures.length) {
                    if (status) status.innerText = "Delete failed";
                    alert(`Some items were not deleted:\n\n${failures.slice(0, 5).join("\n")}${failures.length > 5 ? "\n..." : ""}`);
                } else if (status) {
                    status.innerText = "Done";
                }
                window.refreshFiles();
            } else {
                const res = target.type === "folder"
                    ? await deleteFolder(String(target.id))
                    : await DeleteFile(Number(target.id));

                if (typeof res === "string" && res.startsWith("Error")) {
                    if (status) status.innerText = "Delete failed";
                    alert(res);
                    return;
                }
                if (target.type === "folder") ensureNotInsideDeletedFolder(String(target.id));
                if (status) status.innerText = res || "Done";
                window.refreshFiles();
            }
        } catch (err) {
            console.error("Delete failed:", err);
            if (status) status.innerText = "Delete failed";
            alert("Delete failed. Check console/logs.");
        } finally {
            setTimeout(() => {
                if (status) status.innerText = "Ready";
            }, 2000);
        }
    });
}
