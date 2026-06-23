// Move modal for TDrive frontend

import { state } from '../../state';
import { icons } from '../../constants';
import { MoveFile, MoveFolder, MsgToTdriveSystem } from '../../../wailsjs/go/main/App';
import { callWithPasswordRetry } from './encryption-password';
import { clearSelection } from '../selection';
import { getFolderContents } from '../drive-data';
import { buildFolderIndex, collectDescendants } from '../folder-index';
import { humanizeBackendError } from '../errors';
import { installModalA11y } from './modal-a11y';

let movePath: any[] = [];
let moveBlocked = new Set();
let moveSourceParent = "";
let moveRenderEpoch = 0;
let a11y: ReturnType<typeof installModalA11y> | null = null;

async function ensureFileInTdriveSystem(target: any) {
    if (!target || target.type !== "file") return;
    if (String(target.source || "fs") !== "tg") return;

    const res = await MsgToTdriveSystem(
        Number(target.id),
        String(target.name || ""),
        Number(target.size || 0),
        String(target.parentId || "")
    );

    if (typeof res === "string" && res.startsWith("Error")) {
        throw new Error(humanizeBackendError(res));
    }
}

function getCurrentMoveId() {
    if (!movePath.length) return "";
    return String(movePath[movePath.length - 1]?.id || "");
}

function getCurrentMoveName() {
    if (!movePath.length) return "My Drive";
    return String(movePath[movePath.length - 1]?.name || "Folder");
}

function setMoveError(message: any) {
    const errorEl = document.getElementById("move-error");
    if (!errorEl) return;
    const text = String(message || "").trim();
    errorEl.textContent = text;
    errorEl.style.display = text ? "block" : "none";
}

function isMoveDisabled(destId: any) {
    if (!state.pendingMoveTarget) return true;
    const id = String(destId || "");
    if (moveBlocked && moveBlocked.size && moveBlocked.has(id)) return true;
    if (id === moveSourceParent) return true;
    return false;
}

function updateMoveConfirm() {
    const confirmBtn = document.getElementById("move-confirm") as HTMLButtonElement | null;
    if (!confirmBtn) return;
    const destName = getCurrentMoveName();
    confirmBtn.textContent = `Move to "${destName}"`;
    confirmBtn.disabled = isMoveDisabled(getCurrentMoveId());
}

function renderMoveBreadcrumb() {
    const breadcrumb = document.getElementById("move-breadcrumb");
    const backBtn = document.getElementById("move-back") as HTMLButtonElement | null;
    if (!breadcrumb || !backBtn) return;

    backBtn.disabled = movePath.length === 0;
    backBtn.onclick = () => {
        if (!movePath.length) return;
        movePath = movePath.slice(0, -1);
        renderMoveModal();
    };

    breadcrumb.innerHTML = "";

    const rootBtn = document.createElement("button");
    rootBtn.type = "button";
    rootBtn.className = "move-crumb";
    rootBtn.textContent = "My Drive";
    rootBtn.disabled = movePath.length === 0;
    rootBtn.addEventListener("click", () => {
        if (!movePath.length) return;
        movePath = [];
        renderMoveModal();
    });
    breadcrumb.appendChild(rootBtn);

    movePath.forEach((segment, idx) => {
        const sep = document.createElement("span");
        sep.className = "move-crumb-sep";
        sep.textContent = "/";
        breadcrumb.appendChild(sep);

        const btn = document.createElement("button");
        btn.type = "button";
        btn.className = "move-crumb";
        btn.textContent = String(segment?.name || "Folder");
        btn.disabled = idx === movePath.length - 1;
        btn.addEventListener("click", () => {
            movePath = movePath.slice(0, idx + 1);
            renderMoveModal();
        });
        breadcrumb.appendChild(btn);
    });
}

async function renderMoveList() {
    const list = document.getElementById("move-list");
    if (!list) return;

    const epoch = ++moveRenderEpoch;
    list.innerHTML = '<div class="move-list-empty">Loading folders...</div>';

    let contents;
    try {
        contents = await getFolderContents(getCurrentMoveId());
    } catch {
        contents = { folders: [] };
    }

    if (epoch !== moveRenderEpoch) return;

    const folders = Array.isArray(contents?.folders) ? contents.folders : [];
    folders.sort((a, b) => String(a?.name || "").localeCompare(String(b?.name || "")));

    if (!folders.length) {
        list.innerHTML = '<div class="move-list-empty">No folders here.</div>';
        return;
    }

    list.innerHTML = "";

    folders.forEach((folder) => {
        const id = String(folder?.id || "");
        const name = String(folder?.name || "Folder");
        const disabled = moveBlocked && moveBlocked.has(id);

        const btn = document.createElement("button");
        btn.type = "button";
        btn.className = "move-item";
        if (disabled) {
            btn.classList.add("is-disabled");
            btn.disabled = true;
        }

        const icon = document.createElement("span");
        icon.className = "move-item-icon";
        icon.innerHTML = icons.folder;

        const label = document.createElement("span");
        label.className = "move-item-name";
        label.textContent = name;

        const arrow = document.createElement("span");
        arrow.className = "move-item-arrow";
        arrow.textContent = ">";

        btn.appendChild(icon);
        btn.appendChild(label);
        btn.appendChild(arrow);

        if (!disabled) {
            btn.addEventListener("click", () => {
                movePath = [...movePath, { id, name }];
                renderMoveModal();
            });
        }

        list.appendChild(btn);
    });
}

function renderMoveModal() {
    setMoveError("");
    renderMoveBreadcrumb();
    updateMoveConfirm();
    renderMoveList();
}

export async function openMoveModal(target: any) {
    const modal = document.getElementById("move-modal");
    const title = document.getElementById("move-modal-title");
    const subtitle = document.getElementById("move-modal-subtitle");

    if (!modal || !title || !subtitle) return;

    state.pendingMoveTarget = target;
    movePath = [];
    moveBlocked = new Set();
    moveSourceParent = String(target?.parentId || "");
    moveRenderEpoch = 0;

    if (target?.type === "bulk") {
        const items = Array.isArray(target?.items) ? target.items : [];
        const total = items.length;
        title.textContent = total === 1 ? "Move 1 item" : `Move ${total} items`;
    } else {
        const name = String(target?.name || "").trim();
        title.textContent = name ? `Move "${name}"` : "Move item";
    }
    subtitle.textContent = "Select destination folder";
    setMoveError("");

    modal.style.display = "flex";
    a11y?.activate();

    let index = { children: new Map() };
    try {
        index = await buildFolderIndex();
    } catch {
        index = { children: new Map() };
    }

    if (target?.type === "folder") {
        const folderId = String(target?.id || "");
        if (folderId) {
            const desc = collectDescendants(folderId, index.children);
            const blocked = new Set();
            desc.forEach((id) => blocked.add(String(id)));
            blocked.add(folderId);
            moveBlocked = blocked;
        }
    } else if (target?.type === "bulk") {
        const items = Array.isArray(target?.items) ? target.items : [];
        const folderIds = items
            .filter((item: any) => item?.type === "folder")
            .map((item: any) => String(item?.id || ""))
            .filter(Boolean);

        const union = new Set();
        folderIds.forEach((fid: any) => {
            const desc = collectDescendants(fid, index.children);
            desc.forEach((id) => union.add(String(id)));
            union.add(String(fid));
        });

        moveBlocked = union;
    }

    renderMoveModal();
}

export function setupMoveModal() {
    const modal = document.getElementById("move-modal");
    const cancelBtn = document.getElementById("move-cancel");
    const confirmBtn = document.getElementById("move-confirm") as HTMLButtonElement | null;

    if (!modal || !cancelBtn || !confirmBtn) return;

    const close = () => {
        a11y?.deactivate();
        modal.style.display = "none";
        state.pendingMoveTarget = null;
        movePath = [];
        moveBlocked = new Set();
        moveSourceParent = "";
        setMoveError("");
    };

    cancelBtn.addEventListener("click", close);
    modal.addEventListener("click", (e) => {
        if (e.target === modal) close();
    });

    a11y = installModalA11y(modal, {
        requestClose: close,
        initialFocus: cancelBtn,
        restoreFocus: '#file-list',
    });

    confirmBtn.addEventListener("click", async () => {
        const target = state.pendingMoveTarget;
        if (!target) return;

        const destId = getCurrentMoveId();
        if (isMoveDisabled(destId)) return;

        confirmBtn.disabled = true;
        setMoveError("");

        try {
            if (target.type === "bulk") {
                const items = Array.isArray(target.items) ? target.items : [];
                const folders = items.filter((item: any) => item?.type === "folder");
                const files = items.filter((item: any) => item?.type === "file");

                for (const folder of folders) {
                    const res = await callWithPasswordRetry(() => MoveFolder(String(folder.id), String(destId)));
                    if (typeof res === "string" && res.startsWith("Error")) {
                        throw new Error(humanizeBackendError(res));
                    }
                }

                for (const file of files) {
                    await ensureFileInTdriveSystem(file);
                    const res = await callWithPasswordRetry(() => MoveFile(Number(file.id), String(destId)));
                    if (typeof res === "string" && res.startsWith("Error")) {
                        throw new Error(humanizeBackendError(res));
                    }
                }
            } else if (target.type === "folder") {
                const res = await callWithPasswordRetry(() => MoveFolder(String(target.id), String(destId)));
                if (typeof res === "string" && res.startsWith("Error")) {
                    throw new Error(humanizeBackendError(res));
                }
            } else {
                await ensureFileInTdriveSystem(target);
                const res = await callWithPasswordRetry(() => MoveFile(Number(target.id), String(destId)));
                if (typeof res === "string" && res.startsWith("Error")) {
                    throw new Error(humanizeBackendError(res));
                }
            }

            close();
            clearSelection();
            window.refreshFiles();
        } catch (err) {
            setMoveError(humanizeBackendError(err));
        } finally {
            updateMoveConfirm();
        }
    });
}
