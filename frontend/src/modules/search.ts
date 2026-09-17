import { state } from '../state';
import { formatBytes } from '../utils';
import { clearSelection, deselectRow, handleRowSelection, isRowSelected, selectRow } from './selection';
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
import { getInteractiveFileListRows } from '../ui/file-list/file-list-store';
import { setPhotosMode } from './gallery';
import { getFolderIndexDriveKey, refreshFolderIndex } from './folder-index';
import { canOpenFileViewer, isVideoFile } from './media-types';
import { enqueueDownload, enqueueFolderDownload } from './transfers';
import { appActions } from './app-actions';
import type { FileListAction, FileListRow } from '../ui/file-list/types';
import { getFileList, isMobilePlatform, search } from '../api';
import type { RootFile, SearchHit } from '../types';

let activeToken = 0;
let debounceTimer: ReturnType<typeof setTimeout> | null = null;
let disconnectSearchBar: (() => void) | null = null;
let colDateEl: HTMLElement | null = null;
let telegramRootRequest: Promise<RootFile[]> | null = null;
let telegramRootRequestDriveKey: string | null = null;
const inFlightSearches = new Map<string, Promise<SearchHit[]>>();
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

async function getTelegramRootFiles(driveKey: string): Promise<RootFile[]> {
    if (state.telegramRootCacheDriveKey === driveKey && state.telegramRootCache) {
        return state.telegramRootCache;
    }
    if (telegramRootRequestDriveKey === driveKey && telegramRootRequest) {
        return telegramRootRequest;
    }

    const request = getFileList()
        .then((files) => {
            if ((getFolderIndexDriveKey() ?? 'none') === driveKey) {
                state.telegramRootCache = files;
                state.telegramRootCacheDriveKey = driveKey;
            }
            return files;
        })
        .catch(() => [] as RootFile[]);
    telegramRootRequest = request;
    telegramRootRequestDriveKey = driveKey;
    const clearRequest = () => {
        if (telegramRootRequest === request) {
            telegramRootRequest = null;
            telegramRootRequestDriveKey = null;
        }
    };
    void request.then(clearRequest, clearRequest);
    return request;
}

function searchDrive(query: string, limit: number): Promise<SearchHit[]> {
    const driveKey = getFolderIndexDriveKey() ?? 'none';
    const requestKey = driveKey + '\u0000' + query.trim().toLowerCase() + '\u0000' + String(limit);
    const existing = inFlightSearches.get(requestKey);
    if (existing) return existing;

    const request = search(query, limit);
    inFlightSearches.set(requestKey, request);
    const clearRequest = () => {
        if (inFlightSearches.get(requestKey) === request) inFlightSearches.delete(requestKey);
    };
    void request.then(clearRequest, clearRequest);
    return request;
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
    const sourceChannelId = Number(state.activeChannel?.id ?? 0);
    resultRows.forEach((result) => {
        const type = String(result?.type || "");
        if (type === "folder") {
            const id = String(result.id || "");
            rows.push(buildFolderRow(result, String(result.parentId || ""), {
                key: `search:folder:${id}`,
                channelId: sourceChannelId,
                metaLabel: String(result.path || "My Drive"),
                sizeLabel: "—",
                actions: [{
                    kind: "download",
                    className: "download-folder",
                    title: "Download",
                    label: "Download folder",
                    onClick: () => enqueueFolderDownload(id, String(result?.name || "Folder"), 0, sourceChannelId),
                }],
                onDoubleClick: () => openFolderResult(id),
                onClick: (event) => {
                    const target = event.target as HTMLElement;
                    // Row action buttons are owned by the shared delegated
                    // file-list handler. Treating the overflow button as the
                    // row itself would enter the folder before its sheet can
                    // open on touch devices.
                    if (target.closest("button")) return;
                    const row = target.closest<HTMLElement>('.drive-row');
                    if (!row) return;
                    const logicalRows = getInteractiveFileListRows();
                    if (isMobilePlatform()) {
                        // A touch result follows the same contract as the
                        // drive list: tap opens, while an established
                        // selection turns a tap into an explicit toggle.
                        if (state.selectedItems.size === 0) {
                            // Opening clears the query synchronously. Keep
                            // this click from reaching the generic list
                            // handler after search mode has disappeared.
                            event.stopPropagation();
                            void openFolderResult(id);
                            return;
                        }
                        if (isRowSelected(row)) deselectRow(row);
                        else {
                            const index = logicalRows.findIndex((candidate) => candidate.selectionKey === row.dataset.rowKey);
                            if (index >= 0) selectRow(row, index);
                        }
                        return;
                    }
                    handleRowSelection(row, event, logicalRows);
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
                onClick: () => enqueueDownload(id, name, size, sourceChannelId),
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
                channelId: sourceChannelId,
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
                    const row = target.closest<HTMLElement>('.drive-row');
                    if (!row) return;
                    const logicalRows = getInteractiveFileListRows();
                    if (isMobilePlatform()) {
                        if (state.selectedItems.size === 0) {
                            event.stopPropagation();
                            if (isVideoFile(name)) {
                                void appActions().playVideo({ id, name, size, encrypted });
                            } else if (canOpenFileViewer(name)) {
                                void appActions().openFile({ id, name, size, encrypted });
                            } else {
                                void openFileResult(String(id || ''), String(result.parentId || ''));
                            }
                            return;
                        }
                        if (isRowSelected(row)) deselectRow(row);
                        else {
                            const index = logicalRows.findIndex((candidate) => candidate.selectionKey === row.dataset.rowKey);
                            if (index >= 0) selectRow(row, index);
                        }
                        return;
                    }
                    handleRowSelection(row, event, logicalRows);
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
    cancelScheduledSearch();
    activeToken += 1;
    const input = getSearchInput();
    if (input) input.value = '';
    state.searchQuery = '';
    resetFileListScrollRestore();
    setHeaderMode(false);
    clearSelection();
    if (refresh) appActions().refreshFiles();
}

function setFolderPathAbsolute(folderID: unknown) {
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

async function openFolderResult(folderID: unknown) {
    const id = String(folderID || "");
    if (!id) return;
    clearSearch({ refresh: false });
    setFolderPathAbsolute(id);
    appActions().refreshFiles();
}

async function openFileResult(fileID: unknown, parentID: unknown) {
    const pid = String(parentID || "");
    const fid = String(fileID || "");
    clearSearch({ refresh: false });
    state.pendingFocus = fid ? { type: "file", id: fid } : null;
    setFolderPathAbsolute(pid);
    appActions().refreshFiles();
}

export async function runGlobalSearch() {
    cancelScheduledSearch();
    const query = String(state.searchQuery || '').trim();
    const list = document.getElementById('file-list');
    if (!list) return;
    if (!query) return;

    // Search results render into #file-list, which the Photos view hides. A
    // search is a file-list operation, so leave Photos mode when one runs.
    if (state.virtualView === 'photos') {
        state.virtualView = null;
        setPhotosMode(false);
    }

    const token = ++activeToken;
    const driveKey = getFolderIndexDriveKey() ?? 'none';
    setHeaderMode(true);
    clearSelection();
    renderFileState(list, 'loading', 'Searching files');

    try {
        const [fsResults, tgFiles] = await Promise.all([
            searchDrive(query, 200).catch(() => []),
            getTelegramRootFiles(driveKey),
        ]);
        if (token !== activeToken || (getFolderIndexDriveKey() ?? 'none') !== driveKey) return;

        const normalized = query.toLowerCase();
        const fs = fsResults;

        const fsFileIDs = new Set(
            fs.filter((result) => result.type === 'file').map((result) => result.id),
        );

        const tgMatches: SearchHit[] = tgFiles
            .filter((file) => file.name.toLowerCase().includes(normalized))
            .filter((file) => !fsFileIDs.has(String(file.msgId)))
            .slice(0, 50)
            .map((file) => ({
                type: 'file',
                id: String(file.msgId),
                name: file.name,
                size: file.size,
                parentId: '',
                uploadTime: file.date,
                uploaderId: 0,
                encrypted: false,
                plaintextSize: 0,
                path: 'My Drive',
                source: 'tg',
            }));

        renderSearchResults([...fs, ...tgMatches], query);
    } catch (err) {
        if (token !== activeToken || (getFolderIndexDriveKey() ?? 'none') !== driveKey) return;
        renderFileState(list, 'error', 'Search failed', 'Try again or refine your query.');
        console.error('Search failed:', err);
    }
}

function cancelScheduledSearch() {
    if (debounceTimer) {
        clearTimeout(debounceTimer);
        debounceTimer = null;
    }
}

function scheduleSearch() {
    cancelScheduledSearch();
    // A new input invalidates any request already rendering. The debounce
    // callback will allocate the token for the query it actually runs.
    activeToken += 1;
    const query = String(state.searchQuery || '').trim();
    if (!query) {
        setHeaderMode(false);
        clearSelection();
        appActions().refreshFiles();
        return;
    }
    setHeaderMode(true);
    debounceTimer = setTimeout(() => {
        debounceTimer = null;
        void runGlobalSearch();
    }, 160);
}

export function activateSearchBar(): () => void {
    disconnectSearchBar?.();
    const input = getSearchInput();
    if (!input) return () => {};

    input.value = String(state.searchQuery || '');
    const handleInput = () => {
        state.searchQuery = String(input.value || '');
        scheduleSearch();
    };
    const handleKeydown = (event: KeyboardEvent) => {
        if (event.key === 'Escape') {
            if (!String(state.searchQuery || '').trim()) return;
            event.preventDefault();
            clearSearch();
        } else if (event.key === 'Enter') {
            if (!String(state.searchQuery || '').trim()) return;
            event.preventDefault();
            void runGlobalSearch();
        }
    };
    const handleFindShortcut = (event: KeyboardEvent) => {
        const isFind = (event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'f';
        if (!isFind || document.activeElement === input) return;
        event.preventDefault();
        input.focus();
        input.select();
    };

    input.addEventListener('input', handleInput);
    input.addEventListener('keydown', handleKeydown);
    window.addEventListener('keydown', handleFindShortcut);

    const disconnect = () => {
        if (disconnectSearchBar !== disconnect) return;
        disconnectSearchBar = null;
        cancelScheduledSearch();
        input.removeEventListener('input', handleInput);
        input.removeEventListener('keydown', handleKeydown);
        window.removeEventListener('keydown', handleFindShortcut);
    };
    disconnectSearchBar = disconnect;
    return disconnect;
}
