import { writable } from 'svelte/store';

export type ContextMenuItem =
    | {
        type?: 'item';
        label: string;
        danger?: boolean;
        disabled?: boolean;
        action: () => void | Promise<void>;
    }
    | {
        type: 'divider';
    };

export interface ContextMenuState {
    open: boolean;
    x: number;
    y: number;
    items: ContextMenuItem[];
    focusVersion: number;
}

const initialState: ContextMenuState = {
    open: false,
    x: 0,
    y: 0,
    items: [],
    focusVersion: 0,
};

export const contextMenuState = writable<ContextMenuState>(initialState);
let nextFocusVersion = 0;

export function showContextMenu(x: number, y: number, items: ContextMenuItem[]): void {
    contextMenuState.set({
        open: true,
        x,
        y,
        items,
        focusVersion: ++nextFocusVersion,
    });
}

export function hideContextMenu(): void {
    contextMenuState.set(initialState);
}
