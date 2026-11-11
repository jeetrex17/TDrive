import { get } from 'svelte/store';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';

const storageKey = 'tdrive.fileListSort';

beforeEach(() => {
    const storage = new Map<string, string>();
    vi.stubGlobal('localStorage', {
        getItem: (key: string) => storage.get(key) ?? null,
        setItem: (key: string, value: string) => storage.set(key, value),
        removeItem: (key: string) => storage.delete(key),
    });
});

afterEach(() => {
    vi.unstubAllGlobals();
    vi.resetModules();
});

it('restores the last sort key and direction after restarting without WebView storage', async () => {
    let saved: unknown = null;
    const load = async () => saved;
    const save = vi.fn(async (value: unknown) => { saved = value; });
    const first = await import('./file-sort-store');
    await first.initializeFileSortPersistence(load, save);
    first.setFileSortKey('name');
    first.setFileSortKey('name');
    await vi.waitFor(() => expect(saved).toEqual({ key: 'name', direction: 'desc' }));

    window.localStorage.removeItem(storageKey);
    vi.resetModules();
    const restarted = await import('./file-sort-store');
    await restarted.initializeFileSortPersistence(load, save);

    expect(get(restarted.fileSortState)).toEqual({ key: 'name', direction: 'desc' });
});

it('carries an existing WebView preference into app storage', async () => {
    window.localStorage.setItem(storageKey, JSON.stringify({ key: 'size', direction: 'asc' }));
    const { initializeFileSortPersistence } = await import('./file-sort-store');
    const save = vi.fn(async () => {});
    await initializeFileSortPersistence(async () => null, save);

    await vi.waitFor(() => expect(save).toHaveBeenCalledWith({ key: 'size', direction: 'asc' }));
});
