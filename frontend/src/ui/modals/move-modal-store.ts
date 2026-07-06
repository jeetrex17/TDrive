import { writable } from 'svelte/store';
import { createModalController } from './modal-store';

export interface MoveModalPayload {
    title: string;
}

export interface MoveFolderEntry {
    id: string;
    name: string;
}

export type MoveListing =
    | { status: 'loading' }
    | { status: 'ready'; folders: MoveFolderEntry[] };

// Browse state changes on every navigation, so it lives in its own store
// beside the open/busy/error controller. `blocked` holds the dragged folders
// plus their descendants; `sourceParent` is where the moved items already
// live — both are invalid destinations.
export interface MoveBrowseState {
    path: MoveFolderEntry[];
    listing: MoveListing;
    blocked: ReadonlySet<string>;
    sourceParent: string;
}

const initialBrowse: MoveBrowseState = {
    path: [],
    listing: { status: 'loading' },
    blocked: new Set(),
    sourceParent: '',
};

export const moveModal = createModalController<MoveModalPayload>();
export const moveBrowse = writable<MoveBrowseState>(initialBrowse);

export function resetMoveBrowse(sourceParent: string): void {
    moveBrowse.set({ ...initialBrowse, blocked: new Set(), sourceParent });
}
