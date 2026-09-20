import { writable } from 'svelte/store';

export interface SelectionBarState {
    count: number;
    active?: boolean;
}

export const selectionBarState = writable<SelectionBarState>({ count: 0 });

export function setSelectionCount(count: number): void {
    const normalized = Math.max(0, Math.trunc(count));
    selectionBarState.update((current) => ({
        count: normalized,
        active: Boolean(current.active || normalized > 0),
    }));
}

export function setSelectionModeActive(active: boolean): void {
    selectionBarState.update((current) => ({ ...current, active }));
}
