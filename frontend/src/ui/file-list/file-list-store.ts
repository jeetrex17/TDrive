import { writable } from 'svelte/store';
import type { FileListView, FileListRow, FileListStateView } from './types';

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
