import { writable } from 'svelte/store';

export type FileViewerKind = 'image' | 'audio' | 'pdf' | 'text';

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
    readOnly: boolean;
    protected?: boolean;
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
    readOnly: false,
    protected: false,
};

export const fileViewerState = writable<FileViewerView>(initialState);

export function openFileViewerView(view: Omit<FileViewerView, 'open' | 'readOnly'> & { readOnly?: boolean }): void {
    fileViewerState.set({ open: true, readOnly: false, ...view });
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
