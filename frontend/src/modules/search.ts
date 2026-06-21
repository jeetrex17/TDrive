import { state } from '../state';
import { icons } from '../constants';
import { escapeHtml, splitNameAndExt, formatBytes } from '../utils';
import { clearSelection, handleRowSelection } from './selection';
import { renderBreadcrumb } from './navigation';
import { fillUploaderSlot, resetFileListScrollRestore } from './file-list';
import { setPhotosMode } from './gallery';
import { refreshFolderIndex } from './folder-index';
import { populateUploaderChips } from './uploaders';
import { isVideoFile } from './media-types';

let activeToken = 0;
let debounceTimer: ReturnType<typeof setTimeout> | null = null;
let colDateEl: HTMLElement | null = null;
let colDateText = "";

function setHeaderMode(isSearch: any) {
    const el = colDateEl || document.querySelector(".file-table-header .col-date") as HTMLElement | null;
    if (!el) return;
    colDateEl = el;
    if (!colDateText) colDateText = el.textContent || "";
    el.textContent = isSearch ? "Location" : colDateText;
}

function getSearchInput() {
    return document.getElementById("search-input") as HTMLInputElement | null;
}

async function getTelegramRootFiles() {
    if (Array.isArray(state.telegramRootCache)) return state.telegramRootCache;
    if (!window.go?.main?.App?.GetFileList) return [];
    try {
        const files = await window.go.main.App.GetFileList();
        state.telegramRootCache = Array.isArray(files) ? files : [];
        return state.telegramRootCache;
    } catch {
        return [];
    }
}

async function searchDrive(query: any, limit: any) {
    if (!window.go?.main?.App?.Search) {
        throw new Error("Search is not available. Restart `wails dev` to regenerate bindings.");
    }
    return window.go.main.App.Search(query, limit);
}

function renderSearchResults(results: any, query: any) {
    const list = document.getElementById("file-list");
    if (!list) return;

    const rows = Array.isArray(results) ? results : [];
    if (!rows.length) {
        list.innerHTML = `<div class="search-empty">No results for "${escapeHtml(query)}"</div>`;
        return;
    }

    list.innerHTML = "";

    rows.forEach((result) => {
        const type = String(result?.type || "");
        if (type === "folder") {
            const row = document.createElement("div");
            row.className = "file-row drive-row folder-row";
            row.dataset.type = "folder";
            row.dataset.id = String(result.id || "");
            row.dataset.name = String(result.name || "");
            row.dataset.parentId = String(result.parent_id || "");

            row.innerHTML = `
                <div class="row-name">
                    <span class="folder-chip" aria-hidden="true">${icons.folder}</span>
                    ${escapeHtml(result.name || "Folder")}
                </div>
                <div class="row-meta">${escapeHtml(result.path || "My Drive")}</div>
                <div class="row-meta">—</div>
                <div class="row-actions">
                    <button class="action-icon open-folder" type="button" title="Open">${icons.open}</button>
                </div>
            `;

            row.addEventListener("dblclick", () => openFolderResult(String(result.id || "")));
            row.addEventListener("click", (e) => {
                if ((e.target as HTMLElement).closest("button.open-folder")) return openFolderResult(String(result.id || ""));
                handleRowSelection(row, e);
            });

            list.appendChild(row);
            return;
        }

        if (type === "file") {
            const name = String(result.name || "");
            const { base, ext } = splitNameAndExt(name);
            const row = document.createElement("div");
            row.className = "file-row drive-row";
            row.dataset.type = "file";
            row.dataset.id = String(result.id || "");
            row.dataset.name = name;
            row.dataset.size = String(result.size || 0);
            row.dataset.parentId = String(result.parent_id || "");
            row.dataset.source = String(result.source || "fs");
            row.dataset.uploaderId = String(result.uploader_id || 0);
            row.dataset.uploadTime = String(result.upload_time || 0);
            row.dataset.encrypted = result.encrypted ? "true" : "false";
            // Search results may not be in the active drive; keep
            // owner-only gating consistent: same canOwnerAct heuristic as
            // file-list. We approximate by reading state.activeChannel
            // (search is currently scoped to active drive).
            const ownerOnly = (state.activeChannel?.kind !== "shared")
                || (Number(result.uploader_id || 0) > 0
                    && Number(result.uploader_id) === Number(state.myUserID || 0));
            row.dataset.canDelete = ownerOnly ? "true" : "false";
            row.dataset.canRename = ownerOnly ? "true" : "false";

            row.innerHTML = `
                <div class="row-name">
                    <span class="file-ext-text" aria-hidden="true">${escapeHtml(ext)}</span>
                    ${escapeHtml(base)}
                    <span class="uploader-chip" data-uploader-slot></span>
                </div>
                <div class="row-meta">${escapeHtml(result.path || "My Drive")}</div>
                <div class="row-meta">${formatBytes(Number(result.size || 0))}</div>
                <div class="row-actions">
                    ${isVideoFile(name) ? `<button class="action-icon play-video" type="button" title="Play">${icons.play}</button>` : ""}
                    <button class="action-icon download" type="button" title="Download">${icons.download}</button>
                </div>
            `;
            fillUploaderSlot(row, {
                uploaderID: Number(result.uploader_id || 0),
                uploadTime: Number(result.upload_time || 0),
            });

            const downloadBtn = row.querySelector("button.download");
            if (downloadBtn) {
                downloadBtn.addEventListener("click", () => window.initDownload(Number(result.id || 0), name, Number(result.size || 0)));
            }
            const playBtn = row.querySelector("button.play-video");
            if (playBtn) {
                playBtn.addEventListener("click", () => {
                    window.initVideoPlayback(Number(result.id || 0), name, Number(result.size || 0), Boolean(result.encrypted));
                });
            }

            row.addEventListener("dblclick", () => {
                if (isVideoFile(name)) {
                    window.initVideoPlayback(Number(result.id || 0), name, Number(result.size || 0), Boolean(result.encrypted));
                    return;
                }
                openFileResult(String(result.id || ""), String(result.parent_id || ""));
            });
            row.addEventListener("click", (e) => {
                if ((e.target as HTMLElement).closest("button")) return;
                handleRowSelection(row, e);
            });

            list.appendChild(row);
        }
    });

    populateUploaderChips(list);
}

export function clearSearch({ refresh = true } = {}) {
    const input = getSearchInput();
    if (input) input.value = "";
    state.searchQuery = "";
    resetFileListScrollRestore();
    setHeaderMode(false);
    clearSelection();
    if (refresh) window.refreshFiles();
}

function setFolderPathAbsolute(folderID: any) {
    const id = String(folderID || "");
    if (!id) {
        state.folderPath = [];
        state.currentFolderId = "";
        renderBreadcrumb();
        return;
    }

    state.currentFolderId = id;
    state.folderPath = [];
    renderBreadcrumb();

    refreshFolderIndex()
        .then((index) => {
            if (state.currentFolderId !== id) return;
            const byId = index?.byId;
            if (!(byId instanceof Map)) return;

            const out = [];
            const visited = new Set();
            let cur = id;
            while (cur && !visited.has(cur)) {
                visited.add(cur);
                const folder = byId.get(cur);
                if (!folder) break;
                out.push({ id: folder.id, name: folder.name });
                cur = String(folder.parent_id || "");
            }
            out.reverse();

            if (state.currentFolderId !== id) return;
            state.folderPath = out;
            renderBreadcrumb();
        })
        .catch(() => {});
}

async function openFolderResult(folderID: any) {
    const id = String(folderID || "");
    if (!id) return;
    clearSearch({ refresh: false });
    setFolderPathAbsolute(id);
    window.refreshFiles();
}

async function openFileResult(fileID: any, parentID: any) {
    const pid = String(parentID || "");
    const fid = String(fileID || "");
    clearSearch({ refresh: false });
    state.pendingFocus = fid ? { type: "file", id: fid } : null;
    setFolderPathAbsolute(pid);
    window.refreshFiles();
}

export async function runGlobalSearch() {
    const query = String(state.searchQuery || "").trim();
    const list = document.getElementById("file-list");
    if (!list) return;
    if (!query) return;

    // Search results render into #file-list, which the Photos view hides. A
    // search is a file-list operation, so leave Photos mode when one runs.
    if (state.virtualView === "photos") {
        state.virtualView = null;
        setPhotosMode(false);
    }

    const token = ++activeToken;
    setHeaderMode(true);
    clearSelection();
    list.innerHTML = `<div class="search-empty">Searching…</div>`;

    try {
        const [fsResults, tgFiles] = await Promise.all([
            searchDrive(query, 200).catch(() => []),
            getTelegramRootFiles(),
        ]);
        if (token !== activeToken) return;

        const normalized = query.toLowerCase();
        const fs = Array.isArray(fsResults) ? fsResults : [];

        const fsFileIDs = new Set(
            fs.filter((r) => String(r?.type || "") === "file").map((r) => String(r?.id || ""))
        );

        const tgMatches = Array.isArray(tgFiles)
            ? tgFiles
                .filter((f) => String(f?.name || "").toLowerCase().includes(normalized))
                .filter((f) => !fsFileIDs.has(String(f?.id || "")))
                .slice(0, 50)
                .map((f) => ({
                    type: "file",
                    id: String(f.id),
                    name: String(f.name || ""),
                    size: Number(f.size || 0),
                    parent_id: "",
                    upload_time: Number(f.date || 0),
                    path: "My Drive",
                    source: "tg",
                }))
            : [];

        renderSearchResults([...fs, ...tgMatches], query);
    } catch (err) {
        if (token !== activeToken) return;
        list.innerHTML = `<div class="search-empty">Search failed</div>`;
        console.error("Search failed:", err);
    }
}

function scheduleSearch() {
    if (debounceTimer) clearTimeout(debounceTimer);
    const query = String(state.searchQuery || "").trim();
    if (!query) {
        activeToken += 1;
        setHeaderMode(false);
        clearSelection();
        window.refreshFiles();
        return;
    }
    setHeaderMode(true);
    debounceTimer = setTimeout(() => {
        runGlobalSearch();
    }, 160);
}

export function setupSearchBar() {
    const input = getSearchInput();
    if (!input) return;

    input.value = String(state.searchQuery || "");

    input.addEventListener("input", () => {
        state.searchQuery = String(input.value || "");
        scheduleSearch();
    });

    input.addEventListener("keydown", (e) => {
        if (e.key === "Escape") {
            if (!String(state.searchQuery || "").trim()) return;
            e.preventDefault();
            clearSearch();
        } else if (e.key === "Enter") {
            if (!String(state.searchQuery || "").trim()) return;
            e.preventDefault();
            runGlobalSearch();
        }
    });

    window.addEventListener("keydown", (e) => {
        const isFind = (e.metaKey || e.ctrlKey) && (e.key === "f" || e.key === "F");
        if (!isFind) return;
        if (document.activeElement === input) return;
        e.preventDefault();
        input.focus();
        input.select();
    });
}
