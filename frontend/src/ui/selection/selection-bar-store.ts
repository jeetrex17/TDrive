import { writable } from 'svelte/store';

export interface SelectionBarState {
    count: number;
}

export const selectionBarState = writable<SelectionBarState>({ count: 0 });

export function setSelectionCount(count: number): void {
    selectionBarState.set({ count: Math.max(0, Math.trunc(count)) });
}
