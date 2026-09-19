import { writable } from 'svelte/store';
import {
    DEFAULT_FILE_SORT,
    nextFileSortState,
    type FileSortKey,
    type FileSortState,
} from './file-sort';

const STORAGE_KEY = 'tdrive.fileListSort';

function readInitialSort(): FileSortState {
    if (typeof window === 'undefined') return DEFAULT_FILE_SORT;
    try {
        const raw = window.localStorage.getItem(STORAGE_KEY);
        if (!raw) return DEFAULT_FILE_SORT;
        const parsed = JSON.parse(raw) as Partial<FileSortState>;
        if (!isSortState(parsed)) return DEFAULT_FILE_SORT;
        return parsed;
    } catch {
        return DEFAULT_FILE_SORT;
    }
}

export const fileSortState = writable<FileSortState>(readInitialSort());

if (typeof window !== 'undefined') {
    fileSortState.subscribe((state) => {
        try {
            window.localStorage.setItem(STORAGE_KEY, JSON.stringify(state));
        } catch {
            // Sorting is still usable without persistence.
        }
    });
}

export function setFileSortKey(key: FileSortKey): void {
    fileSortState.update((current) => nextFileSortState(current, key));
}

export function resetFileSortState(): void {
    fileSortState.set(DEFAULT_FILE_SORT);
}

const SORT_KEYS: readonly FileSortKey[] = ['name', 'date', 'size', 'type'];

function isSortState(value: Partial<FileSortState>): value is FileSortState {
    // Guards against a stored preference from an older or newer build: an
    // unrecognised key falls back to the default rather than sorting by
    // nothing, which would look like the list quietly ignoring the choice.
    return (
        SORT_KEYS.includes(value.key as FileSortKey) &&
        (value.direction === 'asc' || value.direction === 'desc')
    );
}
