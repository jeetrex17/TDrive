import { writable } from 'svelte/store';

export type FileViewerKind = 'audio' | 'pdf' | 'text';

export interface FileViewerView {
    open: boolean;
    kind: FileViewerKind | null;
    token: string;
    url: string;
    title: string;
    meta: string;
    mimeType: string;
    loading: boolean;
    error: string;
}

const initialState: FileViewerView = {
    open: false,
    kind: null,
    token: '',
    url: '',
    title: '',
    meta: '',
    mimeType: '',
    loading: false,
    error: '',
};

export const fileViewerState = writable<FileViewerView>(initialState);

export function openFileViewerView(view: Omit<FileViewerView, 'open'>): void {
    fileViewerState.set({ open: true, ...view });
}

export function setFileViewerLoading(loading: boolean): void {
    fileViewerState.update((state) => ({ ...state, loading }));
}

export function setFileViewerError(error: string): void {
    fileViewerState.update((state) => ({ ...state, loading: false, error }));
}

export function closeFileViewerView(): void {
    fileViewerState.set(initialState);
}
