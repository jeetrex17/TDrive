import { writable } from 'svelte/store';

export interface LeaveDriveTarget {
    id: number;
    title: string;
}

export interface LeaveDriveModalState {
    open: boolean;
    inFlight: boolean;
    target: LeaveDriveTarget | null;
}

const initialState: LeaveDriveModalState = {
    open: false,
    inFlight: false,
    target: null,
};

export const leaveDriveModalState = writable<LeaveDriveModalState>(initialState);

export function openLeaveDriveModalView(target: LeaveDriveTarget): void {
    leaveDriveModalState.set({ open: true, inFlight: false, target });
}

export function closeLeaveDriveModalView(): void {
    leaveDriveModalState.set(initialState);
}

export function setLeaveDriveModalInFlight(inFlight: boolean): void {
    leaveDriveModalState.update((state) => ({ ...state, inFlight }));
}
