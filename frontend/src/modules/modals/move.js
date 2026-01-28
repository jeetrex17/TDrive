// Move modal for TDrive frontend

import { state } from '../../state.js';
import { icons } from '../../constants.js';
import { escapeHtml } from '../../utils.js';
import { MoveFile, MoveFolder, MsgToTdriveSystem } from '../../../wailsjs/go/main/App';
import { clearSelection } from '../selection.js';
import { buildFolderIndex, collectDescendants } from '../file-list.js';

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

function getParentPath(folderId, byId) {
    const parents = [];
    let cur = byId.get(folderId);
    let safety = 0;
    while (cur && safety < 50) {
        const pid = cur.parent_id || "";
        const parent = byId.get(pid);
        if (!parent) break;
        parents.push(parent.name || "");
        cur = parent;
        safety += 1;
    }
    parents.reverse();
    return ["My Cloud", ...parents].filter(Boolean).join(" / ");
}

export async function openMoveModal(target) {
    const modal = document.getElementById("move-modal");
    const title = document.getElementById("move-modal-title");
    const subtitle = document.getElementById("move-modal-subtitle");
    const search = document.getElementById("move-search");
    const list = document.getElementById("move-list");
    const errorEl = document.getElementById("move-error");

    if (!modal || !title || !subtitle || !search || !list) return;

    state.pendingMoveTarget = target;
    if (errorEl) {
        errorEl.innerText = "";
        errorEl.style.display = "none";
    }

    title.textContent = "Move to";
    if (target?.type === "bulk") {
        const total = Array.isArray(target?.items) ? target.items.length : 0;
        subtitle.textContent = total === 1 ? "Choose where to move this item." : `Choose where to move ${total} items.`;
    } else {
        subtitle.textContent = target?.type === "folder" ? "Choose where to move this folder." : "Choose where to move this file.";
    }

    search.value = "";
    search.oninput = null;
    list.innerHTML = `<div class="move-empty">Loading folders…</div>`;
    modal.style.display = "flex";

    let index;
    try {
        index = await buildFolderIndex();
    } catch {
        index = { folders: [], byId: new Map(), children: new Map() };
    }

    const sourceParent = String(target?.parentId || "");
    let blocked = new Set();
    if (target?.type === "folder") {
        const movingFolderId = String(target?.id || "");
        if (movingFolderId) {
            blocked = collectDescendants(movingFolderId, index.children);
            blocked.add(movingFolderId);
        }
    } else if (target?.type === "bulk") {
        const items = Array.isArray(target?.items) ? target.items : [];
        const folderIDs = items.filter((i) => i?.type === "folder").map((i) => String(i?.id || "")).filter(Boolean);
        const union = new Set();
        for (const fid of folderIDs) {
            const desc = collectDescendants(fid, index.children);
            desc.add(fid);
            for (const id of desc) union.add(id);
        }
        blocked = union;
    }

    const all = [];
    all.push({ id: "", name: "My Cloud", path: "Root" });
    index.folders.forEach((folder) => {
        all.push({
            id: folder.id,
            name: folder.name || "Folder",
            path: getParentPath(folder.id, index.byId),
        });
    });

    all.sort((a, b) => {
        if (a.id === "") return -1;
        if (b.id === "") return 1;
        const ap = `${a.path} / ${a.name}`.toLowerCase();
        const bp = `${b.path} / ${b.name}`.toLowerCase();
        return ap.localeCompare(bp);
    });

    const render = (q) => {
        const query = (q || "").trim().toLowerCase();
        const items = query
            ? all.filter((item) => (item.name || "").toLowerCase().includes(query) || (item.path || "").toLowerCase().includes(query))
            : all;

        list.innerHTML = "";
        if (!items.length) {
            list.innerHTML = `<div class="move-empty">No folders found.</div>`;
            return;
        }

        items.forEach((item) => {
            const btn = document.createElement("button");
            btn.type = "button";
            btn.className = "move-item";

            const isSameParent = item.id === sourceParent;
            const isBlocked = blocked.size ? blocked.has(item.id) : false;
            const disabled = isSameParent || isBlocked;
            if (disabled) btn.disabled = true;

            let pathText = item.path;
            if (isSameParent) pathText = "Current";
            if (isBlocked) pathText = "Not allowed";

            btn.innerHTML = `
                <span class="move-item-left">
                    <span class="move-item-icon" aria-hidden="true">${icons.folder}</span>
                    <span class="move-item-name">${escapeHtml(item.name)}</span>
                </span>
                <span class="move-item-path">${escapeHtml(pathText)}</span>
            `;

            btn.addEventListener("click", async () => {
                if (!state.pendingMoveTarget) return;
                if (errorEl) {
                    errorEl.innerText = "";
                    errorEl.style.display = "none";
                }

                try {
                    let res = "";
                    if (state.pendingMoveTarget.type === "bulk") {
                        const items = Array.isArray(state.pendingMoveTarget.items) ? state.pendingMoveTarget.items : [];
                        const folders = items.filter((i) => i?.type === "folder");
                        const files = items.filter((i) => i?.type === "file");

                        for (const folder of folders) {
                            res = await MoveFolder(String(folder.id), String(item.id));
                            if (typeof res === "string" && res.startsWith("Error")) {
                                throw new Error(res);
                            }
                        }

                        for (const file of files) {
                            await ensureFileInTdriveSystem(file);
                            res = await MoveFile(Number(file.id), String(item.id));
                            if (typeof res === "string" && res.startsWith("Error")) {
                                throw new Error(res);
                            }
                        }
                    } else if (state.pendingMoveTarget.type === "folder") {
                        res = await MoveFolder(String(state.pendingMoveTarget.id), String(item.id));
                    } else {
                        await ensureFileInTdriveSystem(state.pendingMoveTarget);
                        res = await MoveFile(Number(state.pendingMoveTarget.id), String(item.id));
                    }

                    if (typeof res === "string" && res.startsWith("Error")) {
                        if (errorEl) {
                            errorEl.innerText = res;
                            errorEl.style.display = "block";
                        }
                        return;
                    }

                    const moveModal = document.getElementById("move-modal");
                    if (moveModal) moveModal.style.display = "none";
                    state.pendingMoveTarget = null;
                    clearSelection();
                    window.refreshFiles();
                } catch (err) {
                    if (errorEl) {
                        errorEl.innerText = err?.message || String(err);
                        errorEl.style.display = "block";
                    }
                }
            });

            list.appendChild(btn);
        });
    };

    render("");
    search.oninput = () => render(search.value);

    requestAnimationFrame(() => {
        search.focus();
    });
}

export function setupMoveModal() {
    const modal = document.getElementById("move-modal");
    const cancelBtn = document.getElementById("move-cancel");
    const list = document.getElementById("move-list");
    const search = document.getElementById("move-search");
    const errorEl = document.getElementById("move-error");

    if (!modal || !cancelBtn) return;

    const close = () => {
        modal.style.display = "none";
        state.pendingMoveTarget = null;
        if (list) list.innerHTML = "";
        if (search) {
            search.value = "";
            search.oninput = null;
        }
        if (errorEl) {
            errorEl.innerText = "";
            errorEl.style.display = "none";
        }
    };

    cancelBtn.addEventListener("click", close);
    modal.addEventListener("click", (e) => {
        if (e.target === modal) close();
    });
}
