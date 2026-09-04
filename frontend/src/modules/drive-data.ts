// Thin wrappers over the Wails-generated App bindings for drive data access.
// Guarded so a stale `wails dev` (missing a freshly added binding) fails with
// a clear message instead of an opaque "undefined is not a function".

import type { backend } from '../../wailsjs/go/models';

// `window.go` is the Wails-injected bridge; it has no static type, so access it
// through an untyped view at this single boundary.
function app(): any {
    return (window as any).go?.main?.App;
}

export async function getFolderContents(parentID: string): Promise<backend.FileSystem> {
    const a = app();
    if (a?.GetFolderContents) {
        return a.GetFolderContents(parentID);
    }
    throw new Error("GetFolderContents is not available. Restart `wails dev` to regenerate bindings.");
}

export async function deleteFolder(folderID: string): Promise<string> {
    const a = app();
    if (a?.DeleteFolder) {
        return a.DeleteFolder(folderID);
    }
    throw new Error("DeleteFolder is not available. Restart `wails dev` to regenerate bindings.");
}

export async function createFolder(name: string, parentID: string): Promise<backend.Folder> {
    const a = app();
    if (a?.CreateFolder) {
        return a.CreateFolder(name, parentID);
    }
    throw new Error("CreateFolder is not available. Restart `wails dev` to regenerate bindings.");
}

export async function calculateFolderTotalBytes(folderID: string): Promise<number> {
    const id = String(folderID || "");
    if (!id) return 0;

    const a = app();
    if (a?.GetFolderSize) {
        const value = await a.GetFolderSize(id);
        const bytes = Number(value);
        return Number.isFinite(bytes) && bytes >= 0 ? bytes : 0;
    }
    throw new Error("GetFolderSize is not available. Restart `wails dev` to regenerate bindings.");
}

export type FolderStats = {
    bytes: number;
    latestUpload: number;
};

export async function calculateVisibleFolderStats(parentID: string): Promise<Map<string, FolderStats>> {
    const a = app();
    if (a?.GetFolderStats) {
        const raw = await a.GetFolderStats(String(parentID || ""));
        const out = new Map<string, FolderStats>();
        for (const value of Array.isArray(raw) ? raw : []) {
            const entry = (value ?? {}) as Partial<FolderStats & { id: string }>;
            const id = String(entry.id ?? "");
            if (!id) continue;
            out.set(id, {
                bytes: nonNegative(entry.bytes),
                latestUpload: nonNegative(entry.latestUpload),
            });
        }
        return out;
    }
    throw new Error("GetFolderStats is not available. Restart `wails dev` to regenerate bindings.");
}

function nonNegative(value: unknown): number {
    const n = Number(value);
    return Number.isFinite(n) && n >= 0 ? n : 0;
}

export async function getAllFsMsgIDs(): Promise<number[]> {
    const a = app();
    if (a?.GetAllFsMsgIDs) {
        const ids = await a.GetAllFsMsgIDs();
        return Array.isArray(ids) ? ids : [];
    }
    throw new Error("GetAllFsMsgIDs is not available. Restart `wails dev` to regenerate bindings.");
}
