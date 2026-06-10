// Folder-tree index. Walks the whole folder hierarchy once and exposes it as
// { folders, byId, children } so move/drag can compute a folder's descendants
// (to block dropping a folder into itself or one of its own subfolders).

import { state } from '../state.js';
import { getFolderContents } from './drive-data.js';

export async function buildFolderIndex() {
    const folders = [];
    const byId = new Map();
    const children = new Map();

    const addFolder = (folder) => {
        if (!folder?.id || byId.has(folder.id)) return;
        byId.set(folder.id, folder);
        folders.push(folder);
        const pid = folder.parent_id || "";
        if (!children.has(pid)) children.set(pid, []);
        children.get(pid).push(folder.id);
    };

    const queue = [""];
    const visited = new Set();

    while (queue.length) {
        const parentID = queue.shift();
        if (visited.has(parentID)) continue;
        visited.add(parentID);

        let contents;
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
        const pid = folder.parent_id || "";
        if (!children.has(pid)) children.set(pid, []);
        children.get(pid).sort((a, b) => (byId.get(a)?.name || "").localeCompare(byId.get(b)?.name || ""));
    });

    return { folders, byId, children };
}

export async function refreshFolderIndex() {
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

export function collectDescendants(folderId, children) {
    const out = new Set();
    const stack = [folderId];
    while (stack.length) {
        const id = stack.pop();
        const kids = children.get(id) || [];
        for (const k of kids) {
            if (out.has(k)) continue;
            out.add(k);
            stack.push(k);
        }
    }
    return out;
}
