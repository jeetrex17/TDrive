import { writable } from 'svelte/store';

export const selectedFileRowKeys = writable<ReadonlySet<string>>(new Set());
export const activeFileRowKey = writable('');

export function setSelectedFileRowKeys(keys: Iterable<string>) {
    selectedFileRowKeys.set(new Set(keys));
}

export function setActiveFileRowKey(key: string) {
    activeFileRowKey.set(key);
}
