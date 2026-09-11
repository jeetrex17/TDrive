// Folder-tree index. Walks the whole folder hierarchy once and exposes it as
// { folders, byId, children } so move/drag can compute a folder's descendants
// (to block dropping a folder into itself or one of its own subfolders).

import { state } from '../state';
import { getFolderContents } from '../api';
import type { FolderItem } from '../types';

export interface FolderIndex {
    folders: FolderItem[];
    byId: Map<string, FolderItem>;
    children: Map<string, string[]>;
}

export async function buildFolderIndex(): Promise<FolderIndex> {
    const folders: FolderItem[] = [];
    const byId = new Map<string, FolderItem>();
    const children = new Map<string, string[]>();

    const addFolder = (folder: FolderItem) => {
        if (!folder?.id || byId.has(folder.id)) return;
        byId.set(folder.id, folder);
        folders.push(folder);
        const pid = folder.parentId;
        if (!children.has(pid)) children.set(pid, []);
        children.get(pid)!.push(folder.id);
    };

    const queue: string[] = [""];
    const visited = new Set<string>();

    while (queue.length) {
        const parentID = queue.shift()!;
        if (visited.has(parentID)) continue;
        visited.add(parentID);

        let contents: { folders: FolderItem[] };
        try {
            contents = await getFolderContents(parentID);
        } catch {
            contents = { folders: [] };
        }

        const sub = Array.isArray(contents?.folders) ? contents.folders : [];
        sub.forEach((folder) => {
            addFolder(folder);
            if (folder?.id) queue.push(folder.id);
        });
    }

    folders.forEach((folder) => {
        const pid = folder.parentId;
        if (!children.has(pid)) children.set(pid, []);
        children.get(pid)!.sort((a, b) => (byId.get(a)?.name || "").localeCompare(byId.get(b)?.name || ""));
    });

    return { folders, byId, children };
}

export async function refreshFolderIndex(): Promise<FolderIndex> {
    if (state.folderIndexBuildPromise) return state.folderIndexBuildPromise;
    state.folderIndexBuildPromise = buildFolderIndex()
        .then((idx) => {
            state.folderIndexCache = idx;
            return idx;
        })
        .finally(() => {
            state.folderIndexBuildPromise = null;
        });
    return state.folderIndexBuildPromise;
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
