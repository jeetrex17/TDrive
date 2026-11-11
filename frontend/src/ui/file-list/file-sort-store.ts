import { get, writable } from 'svelte/store';
import {
    DEFAULT_FILE_SORT,
    nextFileSortState,
    type FileSortKey,
    type FileSortState,
} from './file-sort';

const STORAGE_KEY = 'tdrive.fileListSort';
const SORT_KEYS: readonly string[] = ['name', 'date', 'size', 'type'];

function isSortState(value: unknown): value is FileSortState {
    return typeof value === 'object' && value !== null &&
        'key' in value && typeof value.key === 'string' && SORT_KEYS.includes(value.key) &&
        'direction' in value && (value.direction === 'asc' || value.direction === 'desc');
}

function readSavedSort(): FileSortState | null {
    try {
        const parsed: unknown = JSON.parse(window.localStorage.getItem(STORAGE_KEY) ?? 'null');
        return isSortState(parsed) ? parsed : null;
    } catch {
        return null;
    }
}

function rememberInWebView(state: FileSortState): void {
    try {
        window.localStorage.setItem(STORAGE_KEY, JSON.stringify(state));
    } catch {
        // App storage can still persist the preference.
    }
}

export const fileSortState = writable<FileSortState>(readSavedSort() ?? DEFAULT_FILE_SORT);

let saveInApp: ((state: FileSortState) => void) | undefined;

export async function initializeFileSortPersistence(
    load: () => Promise<unknown>,
    save: (state: FileSortState) => Promise<void>,
): Promise<void> {
    let pending = Promise.resolve();
    const initial = get(fileSortState);
    saveInApp = (state) => {
        // Keep rapid clicks in order across asynchronous bridge calls.
        pending = pending.then(() => save(state)).catch((error: unknown) => {
            console.warn('Could not save file sort preference:', error);
        });
    };
    try {
        const saved = await load();
        // A sort picked while the preference loads takes precedence.
        if (get(fileSortState) !== initial) return;
        if (isSortState(saved)) {
            fileSortState.set(saved);
            rememberInWebView(saved);
        } else {
            const legacy = readSavedSort();
            if (legacy) saveInApp(legacy);
        }
    } catch (error) {
        console.warn('Could not load file sort preference:', error);
    }
}

export function setFileSortKey(key: FileSortKey): void {
    const next = nextFileSortState(get(fileSortState), key);
    fileSortState.set(next);
    rememberInWebView(next);
    saveInApp?.(next);
}

export function resetFileSortState(): void {
    fileSortState.set(DEFAULT_FILE_SORT);
}
