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
    pendingDeleteTarget: any;
    pendingRenameTarget: any;
    pendingMoveTarget: any;

    currentFolderId: string;
    folderPath: DrivePathEntry[];

    activeTransfer: "download" | "upload" | null;
    downloadQueue: any[];
    activeDownloadId: string | number | null;

    lastLoginPhoneNumber: string;

    transferPillEl: HTMLElement | null;
    transferSheetEl: HTMLElement | null;
    transferUploadListEl: HTMLElement | null;
    transferClearEl: HTMLElement | null;
    uploadTransfers: Map<string | number, any>;
    uploadBatch: { total: number; done: number; failed: number } | null;
    // Aggregate progress for a folder/archive import. When set, per-file upload
    // events feed this single bell row instead of spawning one row per file.
    importBatch: { total: number; done: number; failed: number } | null;

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
    selectionCountEl: HTMLElement | null;
    selectionMoveBtnEl: HTMLElement | null;
    selectionDeleteBtnEl: HTMLElement | null;
    selectionClearBtnEl: HTMLElement | null;

    activeChannel: Channel | null;
    channels: Channel[];
    pendingJoins: any[];
    channelSwitchInProgress: boolean;
    myUserID: number;
    selfUser: any;

    encryption: EncryptionState;

    virtualView: "photos" | null;

    pendingFolderOps: Map<string, { parentId: string; name: string }>;

    userNames: Map<string, string>;
    userNameFailures: Set<string>;
    userNameRequests: Map<string, Promise<void>>;

    toasts: any[];
    historyEvents: any[];

    notifPanelOpen: boolean;
    notifHoverOpen: boolean;
    notifUnreadErrors: number;
}

export const state: State = {
    pendingDeleteTarget: null,
    pendingRenameTarget: null,
    pendingMoveTarget: null,

    currentFolderId: "",
    folderPath: [],

    activeTransfer: null,
    downloadQueue: [],
    activeDownloadId: null,

    lastLoginPhoneNumber: "",

    transferPillEl: null,
    transferSheetEl: null,
    transferUploadListEl: null,
    transferClearEl: null,
    uploadTransfers: new Map(),
    uploadBatch: null,
    importBatch: null,

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
    selectionCountEl: null,
    selectionMoveBtnEl: null,
    selectionDeleteBtnEl: null,
    selectionClearBtnEl: null,

    activeChannel: null,
    channels: [],
    pendingJoins: [],
    channelSwitchInProgress: false,
    myUserID: 0,
    selfUser: null,

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

    toasts: [],
    historyEvents: [],

    notifPanelOpen: false,
    notifHoverOpen: false,
    notifUnreadErrors: 0,
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
