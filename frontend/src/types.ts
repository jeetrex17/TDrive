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
    path: string;
}

export type MountPhase = 'idle' | 'mounting' | 'mounted' | 'disconnecting' | 'error';
export type MountedDriveKind = 'personal' | 'shared' | 'unknown';

export interface MountedDrive {
    id: number;
    title: string;
    kind: MountedDriveKind;
}

/** Capability-free mount state safe to render in the desktop UI. */
export interface MountStatusView {
    phase: MountPhase;
    mounted: boolean;
    mode: 'read-only';
    label: string;
    location: string;
    error: string;
    drive: MountedDrive | null;
    windowsDrive: string;
}
