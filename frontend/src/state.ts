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

export interface Channel {
    id: number;
    title: string;
    kind: string;
    is_active?: boolean;
    invite_link?: string;
}

export interface EncryptionState {
    available: boolean;
    passwordSet: boolean;
    passwordRemembered: boolean;
    hint: string;
    loaded: boolean;
}

export interface State {
    currentFolderId: string;
    folderPath: DrivePathEntry[];

    activeTransfer: "download" | "upload" | null;
    downloadQueue: any[];
    activeDownloadId: string | number | null;

    transferPillEl: HTMLElement | null;
    transferSheetEl: HTMLElement | null;
    transferUploadListEl: HTMLElement | null;
    transferClearEl: HTMLElement | null;
    uploadTransfers: Map<string | number, any>;
    uploadBatch: { total: number; done: number; failed: number } | null;
    // Aggregate progress for a folder/archive import. When set, per-file upload
    // events feed this single bell row instead of spawning one row per file.
    importBatch: { total: number; done: number; failed: number } | null;
    // True while a cancel is in flight, so abort events relabel rows as canceled
    // instead of failed.
    cancelingUpload: boolean;
    cancelingDownload: boolean;

    dragState: any;
    dragOverEl: HTMLElement | null;
    dragRootEl: HTMLElement | null;

    searchQuery: string;
    telegramRootCache: any[] | null;
    pendingFocus: { type: string; id: string | number } | null;

    folderIndexCache: any;
    folderIndexBuildPromise: any;

    folderSizeEpoch: number;

    selectedItems: Map<string, any>;
    selectionAnchorIndex: number;
    selectionBarEl: HTMLElement | null;

    activeChannel: Channel | null;
    channels: Channel[];
    pendingJoins: any[];
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

    activeTransfer: null,
    downloadQueue: [],
    activeDownloadId: null,

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
    pendingFocus: null,

    folderIndexCache: null,
    folderIndexBuildPromise: null,

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

// Helper to reset folder caches (called on refresh)
export function resetFolderCaches(): void {
    state.folderSizeEpoch += 1;
}

// Helper to reset selection
export function resetSelection(): void {
    state.selectedItems = new Map();
    state.selectionAnchorIndex = -1;
}
