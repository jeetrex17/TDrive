// Move modal for TDrive frontend

import { get } from 'svelte/store';
import { MoveFile, MoveFolder, MsgToTdriveSystem } from '../../../wailsjs/go/main/App';
import { callWithPasswordRetry } from './encryption-password';
import { clearSelection } from '../selection';
import { getFolderContents } from '../drive-data';
import { buildFolderIndex, collectDescendants } from '../folder-index';
import { humanizeBackendError } from '../errors';
import MoveModal from '../../ui/modals/MoveModal.svelte';
import {
    moveBrowse,
    moveModal,
    resetMoveBrowse,
    type MoveFolderEntry,
} from '../../ui/modals/move-modal-store';
import { mountSvelte, type SvelteMountHandle } from '../../ui/mount';

let moveModalHandle: SvelteMountHandle<Record<string, unknown>> | null = null;
let pendingTarget: any = null;
// Guards against out-of-order folder listings and blocked-set results from a
// previous open landing on the current view.
let browseEpoch = 0;

async function ensureFileInTdriveSystem(target: any) {
    if (!target || target.type !== "file") return;
    if (String(target.source || "fs") !== "tg") return;

    const res = await MsgToTdriveSystem(
        Number(target.id),
        String(target.name || ""),
        Number(target.size || 0),
        String(target.parentId || "")
    );

    if (typeof res === "string" && res.startsWith("Error")) {
        throw new Error(humanizeBackendError(res));
    }
}

function moveTitle(target: any): string {
    if (target?.type === "bulk") {
        const total = Array.isArray(target?.items) ? target.items.length : 0;
        return total === 1 ? "Move 1 item" : `Move ${total} items`;
    }
    const name = String(target?.name || "").trim();
    return name ? `Move "${name}"` : "Move item";
}

// browseTo shows `path` immediately (so the breadcrumb tracks the click) and
// fills in that folder's listing when it arrives.
async function browseTo(path: MoveFolderEntry[]): Promise<void> {
    const epoch = ++browseEpoch;
    moveBrowse.update((browse) => ({ ...browse, path, listing: { status: 'loading' } }));

    let contents: { folders?: any[] };
    try {
        contents = await getFolderContents(path[path.length - 1]?.id ?? "");
    } catch {
        contents = { folders: [] };
    }
    if (epoch !== browseEpoch) return;

    const folders = (Array.isArray(contents?.folders) ? contents.folders : [])
        .map((folder: any): MoveFolderEntry => ({
            id: String(folder?.id || ""),
            name: String(folder?.name || "Folder"),
        }))
        .sort((a, b) => a.name.localeCompare(b.name));
    moveBrowse.update((browse) => ({ ...browse, listing: { status: 'ready', folders } }));
}

// computeBlocked marks the moved folders and all their descendants as invalid
// destinations. The index walk is async; the modal is browsable meanwhile and
// the blocked set snaps in when ready.
async function computeBlocked(target: any): Promise<void> {
    const folderIds: string[] = [];
    if (target?.type === "folder") {
        const id = String(target?.id || "");
        if (id) folderIds.push(id);
    } else if (target?.type === "bulk") {
        for (const item of Array.isArray(target?.items) ? target.items : []) {
            if (item?.type !== "folder") continue;
            const id = String(item?.id || "");
            if (id) folderIds.push(id);
        }
    }
    if (!folderIds.length) return;

    const epoch = browseEpoch;
    let index = { children: new Map<string, string[]>() };
    try {
        index = await buildFolderIndex();
    } catch {
        // Keep the empty index: no destinations get blocked, and the backend
        // still rejects a cycle-creating move.
    }
    if (epoch !== browseEpoch || pendingTarget !== target) return;

    const blocked = new Set<string>();
    for (const folderId of folderIds) {
        blocked.add(folderId);
        for (const id of collectDescendants(folderId, index.children)) {
            blocked.add(String(id));
        }
    }
    moveBrowse.update((browse) => ({ ...browse, blocked }));
}

export function setupMoveModal() {
    const modal = document.getElementById("move-modal");
    if (!modal || moveModalHandle) return;

    modal.replaceChildren();
    moveModalHandle = mountSvelte(MoveModal, {
        target: modal,
        props: {
            onOpenFolder: (entry: MoveFolderEntry) => {
                void browseTo([...get(moveBrowse).path, entry]);
            },
            onCrumb: (crumbIndex: number) => {
                const path = get(moveBrowse).path;
                void browseTo(crumbIndex < 0 ? [] : path.slice(0, crumbIndex + 1));
            },
            onBack: () => {
                const path = get(moveBrowse).path;
                if (path.length) void browseTo(path.slice(0, -1));
            },
            onConfirm: confirmMove,
        },
    });
}

export async function openMoveModal(target: any) {
    if (!target) return;

    pendingTarget = target;
    resetMoveBrowse(String(target?.parentId || ""));
    moveModal.open({ title: moveTitle(target) });
    void browseTo([]);
    void computeBlocked(target);
}

async function confirmMove(): Promise<void> {
    const target = pendingTarget;
    if (!target) return;
    const browse = get(moveBrowse);
    const destId = browse.path[browse.path.length - 1]?.id ?? "";
    if (browse.blocked.has(destId) || destId === browse.sourceParent) return;

    moveModal.setError('');
    moveModal.setBusy(true);
    try {
        if (target.type === "bulk") {
            const items = Array.isArray(target.items) ? target.items : [];
            const folders = items.filter((item: any) => item?.type === "folder");
            const files = items.filter((item: any) => item?.type === "file");

            for (const folder of folders) {
                const res = await callWithPasswordRetry(() => MoveFolder(String(folder.id), destId));
                if (typeof res === "string" && res.startsWith("Error")) {
                    throw new Error(humanizeBackendError(res));
                }
            }

            for (const file of files) {
                await ensureFileInTdriveSystem(file);
                const res = await callWithPasswordRetry(() => MoveFile(Number(file.id), destId));
                if (typeof res === "string" && res.startsWith("Error")) {
                    throw new Error(humanizeBackendError(res));
                }
            }
        } else if (target.type === "folder") {
            const res = await callWithPasswordRetry(() => MoveFolder(String(target.id), destId));
            if (typeof res === "string" && res.startsWith("Error")) {
                throw new Error(humanizeBackendError(res));
            }
        } else {
            await ensureFileInTdriveSystem(target);
            const res = await callWithPasswordRetry(() => MoveFile(Number(target.id), destId));
            if (typeof res === "string" && res.startsWith("Error")) {
                throw new Error(humanizeBackendError(res));
            }
        }

        pendingTarget = null;
        moveModal.close();
        clearSelection();
        window.refreshFiles();
    } catch (err) {
        moveModal.setError(humanizeBackendError(err));
    } finally {
        moveModal.setBusy(false);
    }
}
