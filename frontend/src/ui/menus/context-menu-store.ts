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

// Optional header for the mobile action sheet: the item the actions act on.
// Desktop ignores it (the popover has no header). kind picks the leading glyph.
export interface ContextMenuHeader {
    title: string;
    meta?: string;
    kind?: 'file' | 'folder';
}

export interface ContextMenuOptions {
    header?: ContextMenuHeader;
}

export interface ContextMenuState {
    open: boolean;
    x: number;
    y: number;
    items: ContextMenuItem[];
    header: ContextMenuHeader | null;
    focusVersion: number;
}

const initialState: ContextMenuState = {
    open: false,
    x: 0,
    y: 0,
    items: [],
    header: null,
    focusVersion: 0,
};

export const contextMenuState = writable<ContextMenuState>(initialState);
let nextFocusVersion = 0;

// showContextMenu(x, y, items) opens the desktop popover at a point. The
// optional 4th argument carries the action-sheet header used on mobile; the
// file-list long-press handler passes it, the desktop right-click does not.
export function showContextMenu(
    x: number,
    y: number,
    items: ContextMenuItem[],
    options: ContextMenuOptions = {},
): void {
    contextMenuState.set({
        open: true,
        x,
        y,
        items,
        header: options.header ?? null,
        focusVersion: ++nextFocusVersion,
    });
}

export function hideContextMenu(): void {
    contextMenuState.set(initialState);
}
