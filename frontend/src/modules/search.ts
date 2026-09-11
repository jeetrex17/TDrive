import { state } from '../state';
import { formatBytes } from '../utils';
import { clearSelection, handleRowSelection } from './selection';
import { renderBreadcrumb } from './navigation';
import {
    buildFileRow,
    buildFolderRow,
    renderFileListRows,
    renderFileState,
    resetFileListScrollRestore,
    resolveUploaderChipsForRows,
    syncDriveRowTabStops,
} from './file-list';
import { setPhotosMode } from './gallery';
import { refreshFolderIndex } from './folder-index';
import { canOpenFileViewer, isVideoFile } from './media-types';
import { enqueueDownload, enqueueFolderDownload } from './transfers';
import { appActions } from './app-actions';
import type { FileListAction, FileListRow } from '../ui/file-list/types';
import { getFileList, search } from '../api';
import type { RootFile, SearchHit } from '../types';

let activeToken = 0;
let debounceTimer: ReturnType<typeof setTimeout> | null = null;
let colDateEl: HTMLElement | null = null;
let colDateText = "";

function setHeaderMode(isSearch: boolean) {
    const el = colDateEl || document.querySelector(".file-table-header .col-date") as HTMLElement | null;
    if (!el) return;
    colDateEl = el;
    if (!colDateText) colDateText = el.textContent || "";
    el.textContent = isSearch ? "Location" : colDateText;
}

function getSearchInput() {
    return document.getElementById("search-input") as HTMLInputElement | null;
}

async function getTelegramRootFiles(): Promise<RootFile[]> {
    if (state.telegramRootCache) return state.telegramRootCache;
    try {
        state.telegramRootCache = await getFileList();
        return state.telegramRootCache;
    } catch {
        return [];
    }
}

async function searchDrive(query: string, limit: number): Promise<SearchHit[]> {
    return search(query, limit);
}

function renderSearchResults(results: SearchHit[], query: string) {
    const list = document.getElementById("file-list");
    if (!list) return;

    const resultRows = results;
    if (!resultRows.length) {
        renderFileState(list, "empty", `No results for "${String(query || "")}"`);
        return;
    }

    const rows: FileListRow[] = [];
    resultRows.forEach((result) => {
        const type = String(result?.type || "");
        if (type === "folder") {
            const id = String(result.id || "");
            rows.push(buildFolderRow(result, String(result.parentId || ""), {
                key: `search:folder:${id}`,
                metaLabel: String(result.path || "My Drive"),
                sizeLabel: "—",
                actions: [{
                    kind: "download",
                    className: "download-folder",
                    title: "Download",
                    label: "Download folder",
                    onClick: () => enqueueFolderDownload(id, String(result?.name || "Folder")),
                }],
                onDoubleClick: () => openFolderResult(id),
                onClick: (event) => {
                    const target = event.target as HTMLElement;
                    if (target.closest("button.download-folder")) return;
                    const row = target.closest(".drive-row");
                    if (row) handleRowSelection(row, event);
                },
            }));
            return;
        }

        if (type === "file") {
            const name = String(result.name || "");
            // Search results may not be in the active drive; keep
            // owner-only gating consistent: same canOwnerAct heuristic as
            // file-list. We approximate by reading state.activeChannel
            // (search is currently scoped to active drive).
            const ownerOnly = (state.activeChannel?.kind !== "shared")
                || (Number(result.uploaderId || 0) > 0
                    && Number(result.uploaderId) === Number(state.myUserID || 0));
            const id = Number(result.id || 0);
            const size = Number(result.size || 0);
            const encrypted = Boolean(result.encrypted);
            const actions: FileListAction[] = [];
            if (isVideoFile(name)) {
                actions.push({
                    kind: "play",
                    className: "play-video",
                    title: "Play",
                    label: "Play video",
                    onClick: () => { void appActions().playVideo({ id, name, size, encrypted }); },
                });
            } else if (canOpenFileViewer(name)) {
                actions.push({
                    kind: "open",
                    className: "open-file",
                    title: "Open",
                    label: "Open file",
                    onClick: () => { void appActions().openFile({ id, name, size, encrypted }); },
                });
            }
            actions.push({
                kind: "download",
                className: "download",
                title: "Download",
                label: "Download",
                onClick: () => enqueueDownload(id, name, size),
            });
            rows.push(buildFileRow({
                id,
                name,
                size,
                source: String(result.source || "fs"),
                uploaderID: Number(result.uploaderId || 0),
                date: Number(result.uploadTime || 0),
                encrypted,
                canDelete: ownerOnly,
                canRename: ownerOnly,
            }, String(result.parentId || ""), {
                key: `search:file:${String(result.source || "fs")}:${id}`,
                metaLabel: String(result.path || "My Drive"),
                sizeLabel: formatBytes(size),
                actions,
                onDoubleClick: () => {
                    if (isVideoFile(name)) {
                        void appActions().playVideo({ id, name, size, encrypted });
                        return;
                    }
                    if (canOpenFileViewer(name)) {
                        void appActions().openFile({ id, name, size, encrypted });
                        return;
                    }
                    openFileResult(String(id || ""), String(result.parentId || ""));
                },
                onClick: (event) => {
                    const target = event.target as HTMLElement;
                    if (target.closest("button")) return;
                    const row = target.closest(".drive-row");
                    if (row) handleRowSelection(row, event);
                },
            }));
        }
    });

    renderFileListRows(list, rows, () => {
        resolveUploaderChipsForRows(rows, () => String(state.searchQuery || "").trim() === String(query || "").trim());
        syncDriveRowTabStops(list);
    });
}

export function clearSearch({ refresh = true } = {}) {
    const input = getSearchInput();
    if (input) input.value = "";
    state.searchQuery = "";
    resetFileListScrollRestore();
    setHeaderMode(false);
    clearSelection();
    if (refresh) appActions().refreshFiles();
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
                cur = String(folder.parentId || "");
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
    appActions().refreshFiles();
}

async function openFileResult(fileID: any, parentID: any) {
    const pid = String(parentID || "");
    const fid = String(fileID || "");
    clearSearch({ refresh: false });
    state.pendingFocus = fid ? { type: "file", id: fid } : null;
    setFolderPathAbsolute(pid);
    appActions().refreshFiles();
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
    renderFileState(list, "loading", "Searching files");

    try {
        const [fsResults, tgFiles] = await Promise.all([
            searchDrive(query, 200).catch(() => []),
            getTelegramRootFiles(),
        ]);
        if (token !== activeToken) return;

        const normalized = query.toLowerCase();
        const fs = fsResults;

        const fsFileIDs = new Set(
            fs.filter((result) => result.type === "file").map((result) => result.id),
        );

        const tgMatches: SearchHit[] = tgFiles
            .filter((file) => file.name.toLowerCase().includes(normalized))
            .filter((file) => !fsFileIDs.has(String(file.msgId)))
            .slice(0, 50)
            .map((file) => ({
                type: "file",
                id: String(file.msgId),
                name: file.name,
                size: file.size,
                parentId: "",
                uploadTime: file.date,
                uploaderId: 0,
                encrypted: false,
                plaintextSize: 0,
                path: "My Drive",
                source: "tg",
            }));

        renderSearchResults([...fs, ...tgMatches], query);
    } catch (err) {
        if (token !== activeToken) return;
        renderFileState(list, "error", "Search failed", "Try again or refine your query.");
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
        appActions().refreshFiles();
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
