/**
 * The road back from a row element to the row it was drawn from.
 *
 * A click already arrives holding the real row object, but the keyboard,
 * drag-and-drop and context-menu paths only ever hold an `HTMLElement`. They
 * used to recover the row by parsing it back out of eleven `data-*` attributes,
 * which meant every row in the list existed twice: once as a typed object in
 * the store, and once as an untyped string copy in the DOM that was free to
 * disagree with it. A row was whatever the last render happened to serialise,
 * and a field nobody remembered to write was silently an empty string.
 *
 * With this, the element carries one thing -- its key -- and the store stays
 * the only copy of the row.
 */

import { get } from 'svelte/store';
import { fileListView, type InteractiveFileListRow } from './file-list-store';
import type { FileListView } from './types';

/** Every row the list draws, interactive or not. */
export const FILE_ROW_SELECTOR = '.drive-row';

/**
 * Key to row for the currently published view, rebuilt only when that view is
 * replaced.
 *
 * A map rather than a scan of the row array because `dragover` resolves a row
 * on every pointer-move: on a drive with a few thousand files -- the case this
 * app exists for -- a scan per move is a walk of the whole folder between one
 * pixel of mouse travel and the next. The published view is replaced wholesale
 * and never mutated in place, so its object identity is a sound cache key, and
 * an index built from a view that is no longer published can never be served.
 */
let indexedView: FileListView | null = null;
let rowsByKey = new Map<string, InteractiveFileListRow>();

function currentRowsByKey(): ReadonlyMap<string, InteractiveFileListRow> {
    const view = get(fileListView);
    if (view === indexedView) return rowsByKey;

    const next = new Map<string, InteractiveFileListRow>();
    if (view.kind === 'rows') {
        for (const row of view.rows) {
            // First row to claim a key wins, which is what the `.find()` calls
            // this replaced did. Duplicate keys are a bug upstream, but they
            // should at least resolve to the same row every time.
            if (row.kind === 'pending-folder' || next.has(row.selectionKey)) continue;
            next.set(row.selectionKey, row);
        }
    }
    indexedView = view;
    rowsByKey = next;
    return rowsByKey;
}

/** The published row under this key, or null once the list has moved on. */
export function fileListRowForKey(key: string): InteractiveFileListRow | null {
    if (!key) return null;
    return currentRowsByKey().get(key) ?? null;
}

/**
 * The row behind an element: the element itself, or whichever row contains it,
 * which is how an event target from deep inside a row resolves.
 *
 * A detached element resolves to nothing. It belongs to a list that is no
 * longer on screen, and its key may well have been handed to a different row
 * since -- after a drive switch `file:42` is a different file entirely -- so
 * answering would quietly point a delete or a move at the wrong item. The
 * callers all treat null as "no row here" and do nothing, which is the right
 * answer for a row the reader can no longer see.
 */
export function fileListRowForElement(target: Element | null | undefined): InteractiveFileListRow | null {
    const element = target?.closest<HTMLElement>(FILE_ROW_SELECTOR);
    if (!element || !element.isConnected) return null;
    return fileListRowForKey(element.dataset.rowKey ?? '');
}
