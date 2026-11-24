// Back and forward through the folders visited in the active drive, as File
// Explorer's Alt+Left / Alt+Right do. Entries keep only folder ids; a step
// rebuilds the path from the folder index so renames and moves are followed
// and deleted folders are skipped.

import { writable, type Readable } from 'svelte/store';
import { state, type DrivePathEntry } from '../state';
import { refreshFolderIndex } from './folder-index';
import type { FolderIndex } from './folder-index';

interface Place {
    driveId: number;
    folderId: string;
}

const HISTORY_LIMIT = 100;

let entries: Place[] = [];
let position = -1;

export interface FolderHistoryState {
    canGoBack: boolean;
    canGoForward: boolean;
}

const availability = writable<FolderHistoryState>({ canGoBack: false, canGoForward: false });

/** Whether a back or forward step has an entry to try, for the toolbar buttons. */
export const folderHistoryState: Readable<FolderHistoryState> = { subscribe: availability.subscribe };

function publish(): void {
    const next = { canGoBack: position > 0, canGoForward: position >= 0 && position < entries.length - 1 };
    availability.update((current) => (
        current.canGoBack === next.canGoBack && current.canGoForward === next.canGoForward ? current : next
    ));
}

function activeDriveId(): number {
    return Number(state.activeChannel?.id ?? 0);
}

/** Called whenever the breadcrumb changes; ignores repeats and transient states. */
export function recordFolderPlace(): void {
    const driveId = activeDriveId();
    const folderId = state.currentFolderId;
    // Media and Trash are not folders, and search's absolute path arrives in
    // two steps: an empty path first, then the resolved one.
    if (!driveId || state.virtualView !== null) return;
    if ((state.folderPath[state.folderPath.length - 1]?.id ?? '') !== folderId) return;

    const current = entries[position];
    if (current?.driveId === driveId && current.folderId === folderId) return;
    if (current && current.driveId !== driveId) resetFolderHistory();

    // A drive always opens at its root, but the first drive becomes active
    // without a breadcrumb render, so the root may not be recorded yet.
    const start = entries.length === 0 && folderId ? [{ driveId, folderId: '' }] : [];
    entries = [...entries.slice(0, position + 1), ...start, { driveId, folderId }].slice(-HISTORY_LIMIT);
    position = entries.length - 1;
    publish();
}

export function resetFolderHistory(): void {
    entries = [];
    position = -1;
    publish();
}

function pathTo(index: FolderIndex, folderId: string): DrivePathEntry[] | null {
    if (!folderId) return [];
    const path: DrivePathEntry[] = [];
    const visited = new Set<string>();
    for (let id = folderId; id; ) {
        const folder = index.byId.get(id);
        if (!folder || visited.has(id)) return null;
        visited.add(id);
        path.push({ id: folder.id, name: folder.name });
        id = String(folder.parentId || '');
    }
    return path.reverse();
}

let pending: Promise<DrivePathEntry[] | null> | null = null;
let stepsTaken = 0;
let interruptions = 0;

/**
 * Counts the back and forward steps asked for, so work that started before
 * one (a typed path still being read) can see it came later and give way.
 */
export function folderHistorySteps(): number {
    return stepsTaken;
}

/**
 * A newer navigation started: a step still resolving must not take the list
 * away, and must not hold up a Back or Forward pressed after it.
 */
export function interruptFolderHistoryStep(): void {
    interruptions += 1;
    pending = null;
}

/**
 * Moves one step back (-1) or forward (+1) and returns the folder path to
 * show, or null when there is nowhere to go or the reader moved on meanwhile.
 */
export function stepFolderHistory(delta: -1 | 1): Promise<DrivePathEntry[] | null> {
    // A press while the previous step is still resolving is dropped, so a step
    // is never applied twice.
    if (pending) return Promise.resolve(null);
    // Nowhere to go: answered at once, and not counted as a newer choice.
    const driveId = activeDriveId();
    if (!driveId || entries[position]?.driveId !== driveId || !entries[position + delta]) return Promise.resolve(null);
    stepsTaken += 1;
    const run: Promise<DrivePathEntry[] | null> = step(delta).finally(() => {
        // An interrupted step may finish after a newer one started.
        if (pending === run) pending = null;
        publish();
    });
    pending = run;
    return run;
}

async function step(delta: -1 | 1): Promise<DrivePathEntry[] | null> {
    const driveId = activeDriveId();
    const startFolderId = state.currentFolderId;
    const startInterruptions = interruptions;
    if (!driveId || entries[position]?.driveId !== driveId) return null;

    let index: FolderIndex;
    try {
        index = await refreshFolderIndex();
    } catch {
        return null;
    }
    if (activeDriveId() !== driveId || state.currentFolderId !== startFolderId) return null;
    if (interruptions !== startInterruptions) return null;

    for (let target = position + delta; target >= 0 && target < entries.length; ) {
        const path = pathTo(index, entries[target].folderId);
        if (path) {
            position = target;
            return path;
        }
        // The folder is gone: drop it and look one further.
        entries.splice(target, 1);
        if (target < position) position -= 1;
        if (delta < 0) target -= 1;
    }
    return null;
}
