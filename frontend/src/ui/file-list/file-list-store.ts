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
