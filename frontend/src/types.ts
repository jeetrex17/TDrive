// Normalized, camelCase data shapes for the TDrive UI.
//
// Wails serializes the Go structs with snake_case JSON tags (msg_id,
// uploader_id, parent_id, ...). The UI historically read those raw and
// inconsistently — e.g. `f.uploaderId ?? f.uploader_id`, `f.upload_time` in one
// module and `f.uploadTime` in another. These interfaces are the single shape
// the UI should consume; `api.ts` maps each raw Wails response onto them once,
// so nothing downstream has to guess at field names again.

export interface FileItem {
    msgId: number;
    name: string;
    size: number;
    parentId: string;
    uploadTime: number;
    uploaderId: number;
    encrypted: boolean;
    plaintextSize: number;
}

export interface FolderItem {
    id: string;
    name: string;
    parentId: string;
}

export interface FolderContents {
    folders: FolderItem[];
    files: FileItem[];
}

// Root listing comes from the leaner Telegram-backed struct (TDriveFile),
// which keys files by Telegram message id and carries an access hash.
export interface RootFile {
    msgId: number;
    name: string;
    size: number;
    accessHash: number;
    date: number;
}

export type SearchHitType = "file" | "folder";

export interface SearchHit {
    type: SearchHitType;
    id: string;
    name: string;
    parentId: string;
    size: number;
    uploadTime: number;
    uploaderId: number;
    encrypted: boolean;
    plaintextSize: number;
    path: string;
    source: "fs" | "tg";
}

export type DriveKind = "personal" | "shared" | "unknown";

export interface DriveChannel {
    id: number;
    title: string;
    kind: DriveKind;
    isActive: boolean;
    inviteLink: string;
}

export interface PendingJoin {
    inviteHash: string;
    inviteLink: string;
    title: string;
    requestedAt: number;
    lastCheckedAt: number;
    status: string;
    lastError: string;
}

export interface JoinDriveResult {
    status: string;
    channel: DriveChannel | null;
    pending: PendingJoin | null;
}

export interface JoinRequest {
    userId: number;
    displayName: string;
    username: string;
    requestedAt: number;
    about: string;
}

export interface PersonalDriveCandidate {
    id: string;
    title: string;
    createdAt: number;
    hasActivity: boolean;
    recommended: boolean;
}

export interface PersonalDriveSetup {
    status: string;
    activeChannelId: string;
}

export interface SelfUser {
    userId: number;
    displayName: string;
    username: string;
    photoBase64: string;
}

export interface EncryptionStatusView {
    available: boolean;
    passwordSet: boolean;
    passwordRemembered: boolean;
    hint: string;
}

export interface FolderStat {
    id: string;
    bytes: number;
    latestUpload: number;
}

export interface PreviewPayload {
    dataBase64: string;
    mimeType: string;
}

export type DownloadStatus = "success" | "canceled" | "error";

export interface DownloadResult {
    status: DownloadStatus;
    message: string;
    savedPath: string;
}

export interface ImportPlan {
    files: number;
    folders: number;
    bytes: number;
    oversize: number;
    archives: number;
    ignored: number;
    maxBytes: number;
    maxItems: number;
    limitExceeded: boolean;
    errorCount: number;
    errors: string[];
}

export interface AppVersion {
    version: string;
    os: string;
    arch: string;
    devBuild: boolean;
}

export interface UpdateRelease {
    version: string;
    tag: string;
    pageUrl: string;
    publishedAt: string;
    assetName: string;
    assetSize: number;
}

export type UpdatePhase =
    | 'idle'
    | 'disabled'
    | 'checking'
    | 'up_to_date'
    | 'available'
    | 'downloading'
    | 'ready'
    | 'installing'
    | 'installed';

export interface UpdateSnapshot {
    phase: UpdatePhase;
    currentVersion: string;
    latest: UpdateRelease | null;
    installable: boolean;
    installHint: string;
    downloadedBytes: number;
    totalBytes: number;
    checkedAt: number;
    error: string;
    errorStage: string;
}
export type MountPhase = 'idle' | 'mounting' | 'mounted' | 'disconnecting' | 'error';
export type MountedDriveKind = 'personal' | 'shared' | 'unknown';
export type MountMode = 'read-only' | 'read-write';
export type MountWriteState = 'disabled' | 'starting' | 'ready' | 'draining' | 'drained';

export interface MountedDrive {
    id: number;
    title: string;
    kind: MountedDriveKind;
}

export interface MountableDrive {
    id: number;
    title: string;
    kind: 'personal' | 'shared';
}

/** Capability-free mount state safe to render in the desktop UI. */
export interface MountStatusView {
    phase: MountPhase;
    mounted: boolean;
    mode: MountMode;
    writeState: MountWriteState;
    acceptingWrites: boolean;
    activeWrites: number;
    label: string;
    location: string;
    error: string;
    drive: MountedDrive | null;
}
