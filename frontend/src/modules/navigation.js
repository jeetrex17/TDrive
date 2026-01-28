// Breadcrumb and folder navigation for TDrive frontend

import { state } from '../state.js';
import { canDropOnFolder, setDropHighlight, performDropMove } from './drag-drop.js';

export function renderBreadcrumb() {
    const backBtn = document.getElementById("breadcrumb-back");
    const path = document.getElementById("breadcrumb-path");
    if (!backBtn || !path) return;

    backBtn.disabled = state.folderPath.length === 0;
    backBtn.style.opacity = state.folderPath.length === 0 ? "0.35" : "1";

    const items = [
        { id: "", name: "My Drive", index: -1 },
        ...state.folderPath.map((f, i) => ({ id: f.id, name: f.name, index: i })),
    ];
    path.innerHTML = "";

    items.forEach((item, idx) => {
        if (idx > 0) {
            const sep = document.createElement("span");
            sep.className = "breadcrumb-sep";
            sep.textContent = "/";
            path.appendChild(sep);
        }

        const btn = document.createElement("button");
        btn.type = "button";
        btn.className = "breadcrumb-link";
        btn.dataset.index = String(item.index);
        btn.textContent = item.name;
        btn.addEventListener("dragover", (e) => {
            if (!state.dragState) return;
            const allowed = canDropOnFolder(item.id);
            setDropHighlight(btn, allowed);
            if (e.dataTransfer) e.dataTransfer.dropEffect = allowed ? "move" : "none";
            if (allowed) e.preventDefault();
        });
        btn.addEventListener("dragleave", (e) => {
            if (e.relatedTarget && btn.contains(e.relatedTarget)) return;
            if (state.dragOverEl === btn) {
                btn.classList.remove("drop-target");
                btn.classList.remove("drop-denied");
                state.dragOverEl = null;
            }
        });
        btn.addEventListener("drop", async (e) => {
            if (!state.dragState) return;
            const allowed = canDropOnFolder(item.id);
            if (!allowed) return;
            e.preventDefault();
            e.stopPropagation();
            if (state.dragOverEl === btn) state.dragOverEl = null;
            btn.classList.remove("drop-target");
            btn.classList.remove("drop-denied");
            await performDropMove(item.id);
        });

        if (item.index === -1) {
            state.dragRootEl = btn;
        }
        path.appendChild(btn);
    });
}

export function setupBreadcrumb() {
    const backBtn = document.getElementById("breadcrumb-back");
    const path = document.getElementById("breadcrumb-path");
    if (!backBtn || !path) return;

    backBtn.addEventListener("click", () => {
        if (state.folderPath.length === 0) return;
        state.folderPath = state.folderPath.slice(0, -1);
        state.currentFolderId = state.folderPath.length ? state.folderPath[state.folderPath.length - 1].id : "";
        renderBreadcrumb();
        window.refreshFiles();
    });

    path.addEventListener("click", (e) => {
        const btn = e.target.closest("button.breadcrumb-link");
        if (!btn) return;
        const idx = parseInt(btn.dataset.index, 10);
        if (Number.isNaN(idx)) return;

        if (idx < 0) {
            state.folderPath = [];
            state.currentFolderId = "";
        } else {
            state.folderPath = state.folderPath.slice(0, idx + 1);
            state.currentFolderId = state.folderPath[idx]?.id || "";
        }
        renderBreadcrumb();
        window.refreshFiles();
    });

    renderBreadcrumb();
}

export function navigateToFolder(folderID, folderName) {
    state.folderPath = [...state.folderPath, { id: folderID, name: folderName }];
    state.currentFolderId = folderID;
    renderBreadcrumb();
    window.refreshFiles();
}

export function ensureNotInsideDeletedFolder(deletedFolderID) {
    if (!deletedFolderID) return;

    const idx = state.folderPath.findIndex((f) => f.id === deletedFolderID);
    if (idx === -1) return;

    state.folderPath = state.folderPath.slice(0, idx);
    state.currentFolderId = state.folderPath.length ? state.folderPath[state.folderPath.length - 1].id : "";
    renderBreadcrumb();
}
