import { writable } from 'svelte/store';

export interface RenameModalTarget {
    type: 'file' | 'folder';
    id: string | number;
    name: string;
    size?: number;
    parentId?: string;
    source?: string;
}

export interface RenameModalState {
    open: boolean;
    inFlight: boolean;
    error: string;
    target: RenameModalTarget | null;
}

const initialState: RenameModalState = {
    open: false,
    inFlight: false,
    error: '',
    target: null,
};

export const renameModalState = writable<RenameModalState>(initialState);

export function openRenameModalView(target: RenameModalTarget): void {
    renameModalState.set({ open: true, inFlight: false, error: '', target });
}

export function closeRenameModalView(): void {
    renameModalState.set(initialState);
}

export function setRenameModalError(error: string): void {
    renameModalState.update((state) => ({ ...state, error }));
}

export function setRenameModalInFlight(inFlight: boolean): void {
    renameModalState.update((state) => ({ ...state, inFlight }));
}
