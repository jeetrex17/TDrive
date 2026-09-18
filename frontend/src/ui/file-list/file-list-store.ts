import { get, writable } from 'svelte/store';
import { sortFileListRows, type FileSortState } from './file-sort';
import { fileSortState } from './file-sort-store';
import type { FileListFileRow, FileListRow, FileListStateView, FileListView, FolderListRow } from './types';

export const fileListView = writable<FileListView>({
    kind: 'state',
    stateKind: 'loading',
    title: 'Loading files',
});

export function showFileListState(view: Omit<FileListStateView, 'kind'>) {
    fileListView.set({ kind: 'state', ...view });
}

export function showFileListRows(rows: FileListRow[]) {
    fileListView.set({ kind: 'rows', rows });
}

export function updateFileListRows(updater: (rows: FileListRow[]) => FileListRow[]) {
    fileListView.update((view) => {
        if (view.kind !== 'rows') return view;
        const rows = updater(view.rows);
        return rows === view.rows ? view : { kind: 'rows', rows };
    });
}

export type InteractiveFileListRow = FolderListRow | FileListFileRow;

/**
 * The published rows in display order, sorted once per (view, sort) pair.
 *
 * Sorting is an `Intl.Collator` pass over the whole folder -- on the drives
 * this app exists for, six figures of comparisons -- and both the list
 * component and every keyboard, click and selection path want the same array.
 * Recomputing it per caller meant a full re-sort on each arrow-key press, with
 * autorepeat stacking one on top of the next until the list stopped answering.
 *
 * Memoised on the identity of the view and of the sort state. Both are
 * replaced wholesale and never mutated in place -- the same property
 * row-lookup.ts builds its index on -- so identity is a sound cache key, and a
 * result built from a view that is no longer published can never be served.
 *
 * The array is shared with everyone who asks, so it is read-only to all of
 * them.
 */
const NO_ROWS: readonly FileListRow[] = [];

let sortedFrom: FileListView | null = null;
let sortedBy: FileSortState | null = null;
let sortedRows: readonly FileListRow[] = NO_ROWS;

export function sortedFileListRows(view: FileListView, sort: FileSortState): readonly FileListRow[] {
    if (view === sortedFrom && sort === sortedBy) return sortedRows;
    sortedFrom = view;
    sortedBy = sort;
    sortedRows = view.kind === 'rows' ? sortFileListRows(view.rows, sort) : NO_ROWS;
    return sortedRows;
}

/** The same rows minus the placeholders, cached against the array above. */
let interactiveFrom: readonly FileListRow[] | null = null;
let interactiveRows: readonly InteractiveFileListRow[] = [];

export function getInteractiveFileListRows(): readonly InteractiveFileListRow[] {
    const rows = sortedFileListRows(get(fileListView), get(fileSortState));
    if (rows === interactiveFrom) return interactiveRows;
    interactiveFrom = rows;
    interactiveRows = rows.filter(
        (row): row is InteractiveFileListRow => row.kind === 'folder' || row.kind === 'file',
    );
    return interactiveRows;
}
