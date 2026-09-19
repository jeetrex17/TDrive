import { writable } from 'svelte/store';

/**
 * Which glyph an item shows. Named rather than typed as a component so this
 * module stays plain data: the menu component owns the icon set, and a test
 * can assert the action a row offers without importing Svelte.
 */
export type ContextMenuIcon =
    | 'open' | 'play' | 'download' | 'rename' | 'move' | 'delete'
    | 'upload' | 'folder-new' | 'refresh';

export type ContextMenuItem =
    | {
        type?: 'item';
        label: string;
        danger?: boolean;
        disabled?: boolean;
        icon?: ContextMenuIcon;
        /**
         * Promotes the item to a tile above the list on the phone sheet. The
         * desktop popover ignores it and lists everything in order.
         */
        primary?: boolean;
        action: () => void | Promise<void>;
    }
    | {
        type: 'divider';
    };

/** One line of the phone sheet's detail table. */
export interface ContextMenuDetail {
    label: string;
    value: string;
    /** Leading glyph, named the same way menu item icons are. */
    icon?: ContextMenuDetailIcon;
}

export type ContextMenuDetailIcon = 'type' | 'size' | 'added' | 'location';

// Optional header for the mobile action sheet: the item the actions act on.
// Desktop ignores it (the popover has no header). kind picks the leading glyph.
export interface ContextMenuHeader {
    title: string;
    meta?: string;
    kind?: 'file' | 'folder';
    /** Extension, so the sheet shows the same type glyph the row did. */
    ext?: string;
    /** Key/value lines rendered under the actions. */
    details?: readonly ContextMenuDetail[];
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
