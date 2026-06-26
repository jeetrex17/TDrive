import { writable } from 'svelte/store';

export interface FolderModalState {
    open: boolean;
    inFlight: boolean;
}

const initialState: FolderModalState = {
    open: false,
    inFlight: false,
};

export const folderModalState = writable<FolderModalState>(initialState);

export function openFolderModalView(): void {
    folderModalState.set({ open: true, inFlight: false });
}

export function closeFolderModalView(): void {
    folderModalState.set(initialState);
}

export function setFolderModalInFlight(inFlight: boolean): void {
    folderModalState.update((state) => ({ ...state, inFlight }));
}
