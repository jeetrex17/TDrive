import { get, writable } from 'svelte/store';
import { sortFileListRows } from './file-sort';
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

export function getInteractiveFileListRows(): InteractiveFileListRow[] {
    const view = get(fileListView);
    if (view.kind !== 'rows') return [];
    return sortFileListRows(view.rows, get(fileSortState))
        .filter((row): row is InteractiveFileListRow => row.kind === 'folder' || row.kind === 'file');
}
