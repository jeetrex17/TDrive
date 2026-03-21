// Centralized state management for TDrive frontend

export const state = {
    // Delete/Rename/Move modal targets
    pendingDeleteTarget: null, // { type: "file" | "folder" | "bulk", id: number|string, name?: string, items?: array }
    pendingRenameTarget: null,
    pendingMoveTarget: null,

    // Navigation
    currentFolderId: "",
    folderPath: [], // [{ id, name }]

    // Transfer state
    activeTransfer: null, // "download" | "upload" | null
    downloadProgressEl: null,
    downloadProgressFillEl: null,
    downloadProgressHideTimeout: null,
    downloadQueue: [],
    activeDownloadId: null,
    pendingDownloadCancelIds: new Set(),

    // Auth
    lastLoginPhoneNumber: "",

    // Upload transfer UI
    transferPillEl: null,
    transferSheetEl: null,
    transferUploadListEl: null,
    transferClearEl: null,
    uploadTransfers: new Map(), // id -> { id, name, size, parentId, progress, state }
    uploadBatch: null, // { total, done, failed, canceled }
    pendingUploadCancelIds: new Set(),

    // Drag & Drop
    dragState: null,
    dragOverEl: null,
    dragRootEl: null,

    searchQuery: "",
    telegramRootCache: null,
    pendingFocus: null, // { type: "file", id: string }

    // Folder index cache
    folderIndexCache: null,
    folderIndexBuildPromise: null,

    // Folder size cache
    folderSizeEpoch: 0,

    // Selection
    selectedItems: new Map(), // key -> { type, id, name, size, source, parentId, row }
    selectionAnchorIndex: -1,
    selectionBarEl: null,
    selectionCountEl: null,
    selectionMoveBtnEl: null,
    selectionDeleteBtnEl: null,
    selectionClearBtnEl: null,
};

// Helper to reset folder caches (called on refresh)
export function resetFolderCaches() {
    state.folderSizeEpoch += 1;
}

// Helper to reset selection
export function resetSelection() {
    state.selectedItems = new Map();
    state.selectionAnchorIndex = -1;
}
