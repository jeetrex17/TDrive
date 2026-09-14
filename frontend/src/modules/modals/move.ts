// Move modal for TDrive frontend

import { get } from 'svelte/store';
import {
    addTelegramFileToDrive,
    getFolderContents,
    moveFile,
    moveFolder,
    type OperationResult,
} from '../../api';
import { invalidateFolderIndex } from '../../state';
import { callWithPasswordRetry } from './encryption-password';
import { clearSelection } from '../selection';
import { buildFolderIndex, collectDescendants } from '../folder-index';
import { humanizeBackendError } from '../errors';
import { appActions } from '../app-actions';
import MoveModal from '../../ui/modals/MoveModal.svelte';
import {
    moveBrowse,
    moveModal,
    resetMoveBrowse,
    type MoveFolderEntry,
} from '../../ui/modals/move-modal-store';
import type {
    FileCommandItem,
    FileCommandTarget,
    FolderCommandItem,
} from '../../ui/file-list/types';
import { mountSvelte, type SvelteMountHandle } from '../../ui/mount';

let moveModalHandle: SvelteMountHandle<Record<string, unknown>> | null = null;
let pendingTarget: FileCommandTarget | null = null;
let browseEpoch = 0;

function isFolder(item: FileCommandItem): item is FolderCommandItem {
    return item.type === 'folder';
}

function requireOperationSuccess(result: OperationResult): void {
    if (!result.ok) throw new Error(humanizeBackendError(result.error));
}

async function ensureFileInTdriveSystem(target: FileCommandItem): Promise<void> {
    if (target.type !== 'file' || target.source !== 'tg') return;
    requireOperationSuccess(await addTelegramFileToDrive(
        target.id,
        target.name,
        target.size,
        target.parentId,
    ));
}

function moveTitle(target: FileCommandTarget): string {
    if (target.type === 'bulk') {
        return target.items.length === 1 ? 'Move 1 item' : `Move ${target.items.length} items`;
    }
    const name = target.name.trim();
    return name ? `Move "${name}"` : 'Move item';
}

async function browseTo(path: MoveFolderEntry[]): Promise<void> {
    const epoch = ++browseEpoch;
    moveBrowse.update((browse) => ({ ...browse, path, listing: { status: 'loading' } }));

    let folders: MoveFolderEntry[] = [];
    try {
        const contents = await getFolderContents(path[path.length - 1]?.id ?? '');
        folders = contents.folders
            .map((folder) => ({ id: folder.id, name: folder.name || 'Folder' }))
            .sort((left, right) => left.name.localeCompare(right.name));
    } catch {
        folders = [];
    }
    if (epoch !== browseEpoch) return;
    moveBrowse.update((browse) => ({ ...browse, listing: { status: 'ready', folders } }));
}

async function computeBlocked(target: FileCommandTarget): Promise<void> {
    const folderIds = target.type === 'folder'
        ? [target.id]
        : target.type === 'bulk'
            ? target.items.filter(isFolder).map((folder) => folder.id)
            : [];
    if (folderIds.length === 0) return;

    const epoch = browseEpoch;
    let index = { children: new Map<string, string[]>() };
    try {
        index = await buildFolderIndex();
    } catch {
        // The backend remains the final cycle guard if the local index fails.
    }
    if (epoch !== browseEpoch || pendingTarget !== target) return;

    const blocked = new Set<string>();
    for (const folderId of folderIds) {
        blocked.add(folderId);
        for (const id of collectDescendants(folderId, index.children)) blocked.add(String(id));
    }
    moveBrowse.update((browse) => ({ ...browse, blocked }));
}

export function setupMoveModal(): void {
    const modal = document.getElementById('move-modal');
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

export function openMoveModal(target: FileCommandTarget): void {
    pendingTarget = target;
    resetMoveBrowse(target.parentId ?? '');
    moveModal.open({ title: moveTitle(target) });
    void browseTo([]);
    void computeBlocked(target);
}

async function confirmMove(): Promise<void> {
    const target = pendingTarget;
    if (!target) return;
    const browse = get(moveBrowse);
    const destinationId = browse.path[browse.path.length - 1]?.id ?? '';
    if (browse.blocked.has(destinationId) || destinationId === browse.sourceParent) return;

    moveModal.setError('');
    moveModal.setBusy(true);
    try {
        if (target.type === 'bulk') {
            for (const folder of target.items.filter(isFolder)) {
                requireOperationSuccess(await callWithPasswordRetry(
                    () => moveFolder(folder.id, destinationId),
                ));
            }
            for (const file of target.items.filter((item) => item.type === 'file')) {
                await ensureFileInTdriveSystem(file);
                requireOperationSuccess(await callWithPasswordRetry(
                    () => moveFile(file.id, destinationId),
                ));
            }
        } else if (target.type === 'folder') {
            requireOperationSuccess(await callWithPasswordRetry(
                () => moveFolder(target.id, destinationId),
            ));
        } else {
            await ensureFileInTdriveSystem(target);
            requireOperationSuccess(await callWithPasswordRetry(
                () => moveFile(target.id, destinationId),
            ));
        }

        pendingTarget = null;
        moveModal.close();
        clearSelection();
        invalidateFolderIndex();
        appActions().refreshFiles();
    } catch (error) {
        moveModal.setError(humanizeBackendError(error));
    } finally {
        moveModal.setBusy(false);
    }
}
