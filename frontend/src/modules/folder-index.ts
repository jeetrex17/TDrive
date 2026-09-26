// Folder-tree index. Walks the whole folder hierarchy once and exposes it as
// { folders, byId, children } so move/drag can compute a folder's descendants
// (to block dropping a folder into itself or one of its own subfolders).

import { folderIndexGeneration, invalidateFolderIndex as invalidateFolderIndexState, state } from '../state';
import { getFolderContents } from '../api';
import type { FolderItem } from '../types';

export interface FolderIndex {
    folders: FolderItem[];
    byId: Map<string, FolderItem>;
    children: Map<string, string[]>;
}

/** Return the cache key for a drive; channel ids are globally unique. */
export function getFolderIndexDriveKey(driveId: string | number | null | undefined = state.activeChannel?.id): string | null {
    if (driveId === null || driveId === undefined) return null;
    const key = String(driveId).trim();
    return key ? key : null;
}

function traversalError(parentId: string, cause: unknown): Error {
    const location = parentId ? 'folder ' + parentId : 'the drive root';
    const detail = cause instanceof Error && cause.message ? ': ' + cause.message : '';
    return new Error('Could not read ' + location + detail);
}

export async function buildFolderIndex(): Promise<FolderIndex> {
    const folders: FolderItem[] = [];
    const byId = new Map<string, FolderItem>();
    const children = new Map<string, string[]>();

    const addFolder = (folder: FolderItem, parentId: string) => {
        const id = String(folder?.id || '').trim();
        if (!id) throw traversalError(parentId, new Error('backend returned a folder without an id'));
        const existing = byId.get(id);
        if (existing) {
            if (String(existing.parentId || '') !== String(folder.parentId || '')) {
                throw traversalError(parentId, new Error('folder ' + id + ' has conflicting parents'));
            }
            return;
        }

        const normalized = { ...folder, id, parentId: String(folder.parentId || '') };
        byId.set(id, normalized);
        folders.push(normalized);
        const parent = normalized.parentId;
        if (!children.has(parent)) children.set(parent, []);
        children.get(parent)!.push(id);
    };

    const queue: string[] = [''];
    const visited = new Set<string>();

    for (let cursor = 0; cursor < queue.length; cursor += 1) {
        const parentId = queue[cursor];
        if (visited.has(parentId)) continue;
        visited.add(parentId);

        let contents: Awaited<ReturnType<typeof getFolderContents>>;
        try {
            contents = await getFolderContents(parentId);
        } catch (error) {
            throw traversalError(parentId, error);
        }

        if (!contents || !Array.isArray(contents.folders)) {
            throw traversalError(parentId, new Error('backend returned an incomplete folder listing'));
        }
        for (const folder of contents.folders) {
            if (!folder || typeof folder !== 'object') {
                throw traversalError(parentId, new Error('backend returned an invalid folder entry'));
            }
            const folderParentId = String(folder.parentId || '');
            if (folderParentId !== parentId) {
                throw traversalError(parentId, new Error('backend returned a folder with an incorrect parent'));
            }
            addFolder(folder, parentId);
            queue.push(String(folder.id));
        }
    }

    folders.forEach((folder) => {
        const parent = String(folder.parentId || '');
        if (!children.has(parent)) children.set(parent, []);
        children.get(parent)!.sort((a, b) => (byId.get(a)?.name || '').localeCompare(byId.get(b)?.name || ''));
    });

    return { folders, byId, children };
}

export function refreshFolderIndex(): Promise<FolderIndex> {
    const driveKey = getFolderIndexDriveKey();
    if (!driveKey) return buildFolderIndex();

    if (state.folderIndexCacheDriveKey === driveKey && state.folderIndexCache) {
        return Promise.resolve(state.folderIndexCache);
    }
    if (state.folderIndexBuildDriveKey === driveKey && state.folderIndexBuildPromise) {
        return state.folderIndexBuildPromise;
    }

    const generation = folderIndexGeneration(driveKey);
    const buildPromise = buildFolderIndex().then((index) => {
        // An invalidation, drive switch, or newer build can supersede this request.
        // Only the still-current generation may publish into shared state.
        if (
            state.folderIndexBuildPromise === buildPromise
            && state.folderIndexBuildDriveKey === driveKey
            && state.folderIndexBuildGeneration === generation
            && folderIndexGeneration(driveKey) === generation
        ) {
            state.folderIndexCache = index;
            state.folderIndexCacheDriveKey = driveKey;
        }
        return index;
    });

    state.folderIndexBuildPromise = buildPromise;
    state.folderIndexBuildDriveKey = driveKey;
    state.folderIndexBuildGeneration = generation;

    const clearBuild = () => {
        // Do not let an old build's finally-like cleanup clear a newer promise.
        if (
            state.folderIndexBuildPromise === buildPromise
            && state.folderIndexBuildDriveKey === driveKey
            && state.folderIndexBuildGeneration === generation
        ) {
            state.folderIndexBuildPromise = null;
            state.folderIndexBuildDriveKey = null;
            state.folderIndexBuildGeneration = 0;
        }
    };
    void buildPromise.then(clearBuild, clearBuild);
    return buildPromise;
}

export function invalidateFolderIndex(driveKey?: string | number | null): void {
    invalidateFolderIndexState(driveKey);
}

export function collectDescendants(folderId: string, children: Map<string, string[]>): Set<string> {
    const out = new Set<string>();
    const stack: string[] = [folderId];
    while (stack.length) {
        const id = stack.pop()!;
        const kids = children.get(id) || [];
        for (const k of kids) {
            if (out.has(k)) continue;
            out.add(k);
            stack.push(k);
        }
    }
    return out;
}
