import type { ImportProgress } from './modules/import-progress';
import type { DriveChannel, DriveKind, PendingJoin, RootFile } from './types';
import type { FileCommandItem, FileDragState } from './ui/file-list/types';

// Centralized state for the TDrive frontend.
//
// Loosely-shaped, frequently-reshaped fields (drag payloads, modal targets,
// transient caches) are typed `any` on purpose — they are read/written from
// many still-untyped modules, and over-specifying them here would just produce
// churn. The value of this type is catching field-name typos and locking the
// primitive/collection shapes.

export interface DrivePathEntry {
    id: string;
    name: string;
}


export interface EncryptionState {
    available: boolean;
    passwordSet: boolean;
    passwordRemembered: boolean;
    hint: string;
    loaded: boolean;
}

export type DownloadState = 'queued' | 'downloading';

export type TransferDirection = 'upload' | 'download';

export interface TransferActivity {
    readonly upload: boolean;
    readonly download: boolean;
}

export const idleTransferActivity: TransferActivity = Object.freeze({
    upload: false,
    download: false,
});

type DownloadQueueBase = {
    key: string;
    // The backend must never infer this from the currently selected drive:
    // queued work can start after the user has changed drives.
    channelId: number;
    name: string;
    size: number;
    progress: number;
    state: DownloadState;
    bytesCompleted: number;
    bytesTotal: number;
    filesCompleted: number;
    filesTotal: number;
};

export type FileDownloadQueueItem = DownloadQueueBase & {
    kind: 'file';
    id: number;
};

export type FolderDownloadQueueItem = DownloadQueueBase & {
    kind: 'folder';
    id: string;
};

export type DownloadQueueItem = FileDownloadQueueItem | FolderDownloadQueueItem;

export interface State {
    currentFolderId: string;
    folderPath: DrivePathEntry[];

    transferActivity: TransferActivity;
    downloadQueue: DownloadQueueItem[];
    activeDownloadId: string | null;
    // Fresh for every backend dispatch, even when retrying the same queue key.
    activeDownloadRequestId: string | null;

    transferPillEl: HTMLElement | null;
    transferSheetEl: HTMLElement | null;
    transferUploadListEl: HTMLElement | null;
    transferClearEl: HTMLElement | null;
    uploadTransfers: Map<string | number, any>;
    uploadBatch: { total: number; done: number; failed: number } | null;
    // Constant-size aggregate progress for a folder/archive import. The backend
    // coalesces per-file activity before it crosses the Wails bridge.
    importBatch: ImportProgress | null;
    // True while a cancel is in flight, so abort events relabel rows as canceled
    // instead of failed.
    cancelingUpload: boolean;
    cancelingDownload: boolean;

    dragState: FileDragState | null;
    dragOverEl: HTMLElement | null;
    dragRootEl: HTMLElement | null;

    searchQuery: string;
    telegramRootCache: RootFile[] | null;
    telegramRootCacheDriveKey: string | null;
    pendingFocus: { type: string; id: string | number } | null;

    folderIndexCache: any;
    folderIndexBuildPromise: any;

    folderIndexCacheDriveKey: string | null;
    folderIndexBuildDriveKey: string | null;
    folderIndexBuildGeneration: number;
    folderIndexGenerations: Map<string, number>;
    folderSizeEpoch: number;

    selectedItems: Map<string, FileCommandItem>;
    selectionAnchorIndex: number;
    selectionBarEl: HTMLElement | null;

    activeChannel: { id: number; title: string; kind: DriveKind } | null;
    channels: DriveChannel[];
    pendingJoins: PendingJoin[];
    channelSwitchInProgress: boolean;
    myUserID: number;

    encryption: EncryptionState;

    virtualView: "photos" | null;

    pendingFolderOps: Map<string, { parentId: string; name: string }>;

    userNames: Map<string, string>;
    userNameFailures: Set<string>;
    userNameRequests: Map<string, Promise<void>>;
}

export const state: State = {
    currentFolderId: "",
    folderPath: [],

    transferActivity: idleTransferActivity,
    downloadQueue: [],
    activeDownloadId: null,
    activeDownloadRequestId: null,

    transferPillEl: null,
    transferSheetEl: null,
    transferUploadListEl: null,
    transferClearEl: null,
    uploadTransfers: new Map(),
    uploadBatch: null,
    importBatch: null,
    cancelingUpload: false,
    cancelingDownload: false,

    dragState: null,
    dragOverEl: null,
    dragRootEl: null,

    searchQuery: "",
    telegramRootCache: null,
    telegramRootCacheDriveKey: null,
    pendingFocus: null,

    folderIndexCache: null,
    folderIndexBuildPromise: null,

    folderIndexCacheDriveKey: null,
    folderIndexBuildDriveKey: null,
    folderIndexBuildGeneration: 0,
    folderIndexGenerations: new Map(),
    folderSizeEpoch: 0,

    selectedItems: new Map(),
    selectionAnchorIndex: -1,
    selectionBarEl: null,

    activeChannel: null,
    channels: [],
    pendingJoins: [],
    channelSwitchInProgress: false,
    myUserID: 0,

    encryption: {
        available: false,
        passwordSet: false,
        passwordRemembered: false,
        hint: "",
        loaded: false,
    },

    virtualView: null,

    pendingFolderOps: new Map(),

    userNames: new Map(),
    userNameFailures: new Set(),
    userNameRequests: new Map(),
};

// Transfer direction flags are replaced together rather than mutated so a
// completion in one direction never clears concurrent activity in the other.
export function setTransferDirectionActive(direction: TransferDirection, active: boolean): void {
    const current = state.transferActivity;
    if (current[direction] === active) return;
    state.transferActivity = Object.freeze({ ...current, [direction]: active });
}

function normalizeFolderIndexDriveKey(driveKey: string | number | null | undefined): string | null {
    if (driveKey === null || driveKey === undefined) return null;
    const key = String(driveKey).trim();
    return key ? key : null;
}

function bumpFolderIndexGeneration(driveKey: string): number {
    const generation = (state.folderIndexGenerations.get(driveKey) ?? 0) + 1;
    state.folderIndexGenerations.set(driveKey, generation);
    return generation;
}

/**
 * Invalidate the recursive folder index for one drive without invalidating
 * unrelated drive state. A build is allowed to finish for its existing
 * callers, but its generation no longer permits publishing into the cache.
 */
export function invalidateFolderIndex(driveKey?: string | number | null): void {
    const key = normalizeFolderIndexDriveKey(driveKey === undefined ? state.activeChannel?.id : driveKey);
    if (!key) {
        const staleKeys = [state.folderIndexCacheDriveKey, state.folderIndexBuildDriveKey]
            .filter((value): value is string => Boolean(value));
        for (const staleKey of new Set(staleKeys)) bumpFolderIndexGeneration(staleKey);
        state.folderIndexCache = null;
        state.folderIndexCacheDriveKey = null;
        state.folderIndexBuildPromise = null;
        state.folderIndexBuildDriveKey = null;
        state.folderIndexBuildGeneration = 0;
        return;
    }

    bumpFolderIndexGeneration(key);
    if (state.folderIndexCacheDriveKey === key) {
        state.folderIndexCache = null;
        state.folderIndexCacheDriveKey = null;
    }
    if (state.folderIndexBuildDriveKey === key) {
        state.folderIndexBuildPromise = null;
        state.folderIndexBuildDriveKey = null;
        state.folderIndexBuildGeneration = 0;
    }
}

export function folderIndexGeneration(driveKey: string | number): number {
    return state.folderIndexGenerations.get(String(driveKey)) ?? 0;
}

// Helper to reset folder caches (called on refresh)
export function resetFolderCaches(): void {
    state.folderSizeEpoch += 1;
}

// Helper to reset selection
export function resetSelection(): void {
    state.selectedItems = new Map();
    state.selectionAnchorIndex = -1;
}
