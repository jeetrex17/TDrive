import { writable } from 'svelte/store';

export interface SelectionBarState {
    count: number;
    /** Total size of the selection, or null when it holds a folder of unknown size. */
    bytes?: number | null;
    active?: boolean;
}

export const selectionBarState = writable<SelectionBarState>({ count: 0 });

export function setSelectionCount(count: number, bytes: number | null = null): void {
    const normalized = Math.max(0, Math.trunc(count));
    selectionBarState.update((current) => ({
        count: normalized,
        bytes,
        active: Boolean(current.active || normalized > 0),
    }));
}

export function setSelectionModeActive(active: boolean): void {
    selectionBarState.update((current) => ({ ...current, active }));
}
