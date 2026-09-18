// Trash state and the three mutations, in one place. The surface is the same
// on both shells, so the stores live here rather than in either component:
// the desktop dialog and the phone sheet render the identical controller.
import { get, writable } from 'svelte/store';
import {
    deleteFromTrashPermanently, emptyTrash, listTrash, restoreFromTrash, type TrashEntry,
} from '../../api/trash';
import { sortedByDeletion, trashSummary } from '../../ui/trash/trash-view';
import { trashConfirmModal } from '../../ui/trash/trash-confirm-store';
import type { OperationResult } from '../../types';
import { invalidateFolderIndex, state } from '../../state';
import { clearSearch } from '../search';
import { renderTrashError, renderTrashRows } from './view';
import { appActions } from '../app-actions';
import { humanizeBackendError } from '../errors';
import { notify } from '../notifications';

/** The busy key for an operation that spans the whole trash, not one row. */
export const EMPTY_TRASH_KEY = '*';

export type TrashStatus = 'idle' | 'loading' | 'ready' | 'error';

export const trashOpen = writable(false);
/** Always sorted newest-deleted first; nothing downstream re-sorts. */
export const trashEntries = writable<TrashEntry[]>([]);
export const trashStatus = writable<TrashStatus>('idle');
export const trashError = writable('');
/**
 * The object id of the row whose mutation is in flight, EMPTY_TRASH_KEY while
 * the whole trash is being emptied, or '' when idle. One key is enough: the
 * panel locks every control for the duration, so two can never overlap.
 */
export const trashBusyKey = writable('');

// A load that is no longer wanted must not overwrite a newer one. The dialog
// can be closed and reopened faster than a slow backend answers.
let loadGeneration = 0;

/**
 * Enters the trash. It is a destination in the main area rather than a dialog,
 * so it goes through the same virtual-view switch Photos uses and lets
 * refreshFiles decide what to draw.
 */
export function openTrash(): void {
    if (state.virtualView === 'trash') return;
    trashOpen.set(true);
    state.virtualView = 'trash';
    clearSearch();
    appActions().refreshFiles();
    void loadTrash();
}

/** Leaves the trash for the folder the drive was last showing. */
export function closeTrash(): void {
    loadGeneration += 1;
    trashConfirmModal.close();
    trashOpen.set(false);
    trashBusyKey.set('');
    if (state.virtualView !== 'trash') return;
    state.virtualView = null;
    appActions().refreshFiles();
}

export async function loadTrash(): Promise<void> {
    const generation = ++loadGeneration;
    trashStatus.set('loading');
    trashError.set('');
    try {
        const entries = await listTrash();
        if (generation !== loadGeneration) return;
        trashEntries.set(sortedByDeletion(entries));
        trashStatus.set('ready');
        publishTrashRows();
    } catch (error) {
        if (generation !== loadGeneration) return;
        trashEntries.set([]);
        trashStatus.set('error');
        trashError.set(humanizeBackendError(error));
        publishTrashRows();
    }
}

export async function restoreEntry(objectId: string): Promise<void> {
    if (await mutate(objectId, () => restoreFromTrash(objectId))) {
        dropEntry(objectId);
        // The item is back in a folder the user may be looking at.
        invalidateFolderIndex();
        refreshDrive();
    }
}

/** Both destructive paths ask first; neither ever runs off the row's button. */
export function askPurgeTrashEntry(entry: TrashEntry): void {
    trashConfirmModal.open({
        kind: 'purge', objectId: entry.objectId, name: entry.name, isFolder: entry.kind === 'folder',
    });
}

export function askEmptyTrash(): void {
    trashConfirmModal.open({ kind: 'empty', summary: trashSummary(get(trashEntries)) });
}

/**
 * Runs whatever was confirmed. The question closes before the work starts, so
 * a slow backend cannot leave a confirmable dialog on screen; the list
 * underneath reports the outcome either way.
 */
export async function confirmTrashAction(): Promise<void> {
    const target = get(trashConfirmModal.state).payload;
    trashConfirmModal.close();
    if (!target) return;
    if (target.kind === 'empty') {
        if (await mutate(EMPTY_TRASH_KEY, emptyTrash)) {
            trashEntries.set([]);
            publishTrashRows();
        }
        return;
    }
    if (await mutate(target.objectId, () => deleteFromTrashPermanently(target.objectId))) {
        dropEntry(target.objectId);
    }
}

/**
 * Runs one mutation under the busy key and reports whether it succeeded. A
 * refusal is shown in the backend's own words; only a thrown call error is
 * humanized here.
 */
async function mutate(key: string, run: () => Promise<OperationResult>): Promise<boolean> {
    if (get(trashBusyKey)) return false;
    trashBusyKey.set(key);
    trashError.set('');
    try {
        const result = await run();
        if (!result.ok) {
            reportMutationRefusal(result.error.message);
            return false;
        }
        return true;
    } catch (error) {
        reportMutationRefusal(humanizeBackendError(error));
        return false;
    } finally {
        trashBusyKey.set('');
    }
}

/**
 * A refused restore or delete is reported beside the list, not in place of it.
 *
 * The list is still correct -- the item really is still in the trash -- so
 * replacing it with an error state would throw away a good answer to say that a
 * different request failed. As a dialog this was an inline alert; as a full view
 * the equivalent is a toast.
 */
function reportMutationRefusal(message: string): void {
    trashError.set(message);
    if (state.virtualView !== 'trash') return;
    notify({ level: 'error', title: 'Trash', body: message });
}

/**
 * Drops one row locally instead of re-listing. The backend has confirmed the
 * item left the trash, so a round trip would only make the list flicker.
 */
function dropEntry(objectId: string): void {
    trashEntries.update((entries) => entries.filter((entry) => entry.objectId !== objectId));
    publishTrashRows();
}

/**
 * Redraws the list, but only while the trash is the thing on screen. Every
 * mutation calls it, so a restore that lands after the user has already left
 * cannot publish trash rows over the folder they went back to.
 */
function publishTrashRows(): void {
    if (state.virtualView !== 'trash') return;
    if (get(trashStatus) === 'error') {
        renderTrashError(get(trashError));
        return;
    }
    renderTrashRows();
}

/**
 * Asks the file list to re-read the current folder. The actions are configured
 * with the dashboard; a trash opened before that is a no-op refresh rather
 * than a failed restore.
 */
function refreshDrive(): void {
    try {
        appActions().refreshFiles();
    } catch {
        // No dashboard to refresh yet.
    }
}
