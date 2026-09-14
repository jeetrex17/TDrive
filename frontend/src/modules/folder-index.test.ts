import { beforeEach, describe, expect, it, vi } from 'vitest';

const getFolderContentsMock = vi.hoisted(() => vi.fn());

vi.mock('../api', () => ({
    getFolderContents: getFolderContentsMock,
}));

import { state, invalidateFolderIndex } from '../state';
import { refreshFolderIndex } from './folder-index';

function deferred<T>() {
    let resolve!: (value: T) => void;
    let reject!: (reason?: unknown) => void;
    const promise = new Promise<T>((resolvePromise, rejectPromise) => {
        resolve = resolvePromise;
        reject = rejectPromise;
    });
    return { promise, resolve, reject };
}

function resetFolderIndexState() {
    state.activeChannel = { id: 1, title: 'Personal', kind: 'personal' };
    state.folderIndexCache = null;
    state.folderIndexCacheDriveKey = null;
    state.folderIndexBuildPromise = null;
    state.folderIndexBuildDriveKey = null;
    state.folderIndexBuildGeneration = 0;
    state.folderIndexGenerations = new Map();
    getFolderContentsMock.mockReset();
}

describe('folder index', () => {
    beforeEach(resetFolderIndexState);

    it('deduplicates concurrent builds and reuses a complete drive-scoped cache', async () => {
        const root = deferred<{ folders: Array<{ id: string; name: string; parentId: string }> }>();
        getFolderContentsMock.mockImplementation((parentId: string) => (
            parentId === '' ? root.promise : Promise.resolve({ folders: [] })
        ));

        const first = refreshFolderIndex();
        const second = refreshFolderIndex();
        expect(second).toBe(first);
        expect(getFolderContentsMock).toHaveBeenCalledTimes(1);

        root.resolve({ folders: [{ id: 'docs', name: 'Docs', parentId: '' }] });
        const index = await first;
        expect(index.byId.get('docs')?.name).toBe('Docs');
        expect(getFolderContentsMock).toHaveBeenCalledTimes(2);

        const cached = await refreshFolderIndex();
        expect(cached).toBe(index);
        expect(getFolderContentsMock).toHaveBeenCalledTimes(2);
    });

    it('does not reuse an index from another active drive', async () => {
        getFolderContentsMock.mockImplementation((parentId: string) => (
            Promise.resolve({
                folders: parentId === ''
                    ? [{ id: String(state.activeChannel?.id) + '-folder', name: 'Folder', parentId: '' }]
                    : [],
            })
        ));

        const first = await refreshFolderIndex();
        state.activeChannel = { id: 2, title: 'Shared', kind: 'shared' };
        const second = await refreshFolderIndex();

        expect(second).not.toBe(first);
        expect(second.byId.has('2-folder')).toBe(true);
        expect(getFolderContentsMock).toHaveBeenCalledTimes(4);
    });

    it('fails instead of caching a partial subtree when traversal is incomplete', async () => {
        getFolderContentsMock.mockImplementation((parentId: string) => {
            if (parentId === '') return Promise.resolve({ folders: [{ id: 'broken', name: 'Broken', parentId: '' }] });
            return Promise.reject(new Error('network unavailable'));
        });

        await expect(refreshFolderIndex()).rejects.toThrow('Could not read folder broken: network unavailable');
        expect(state.folderIndexCache).toBeNull();
        expect(state.folderIndexCacheDriveKey).toBeNull();
    });

    it('guards the current build promise when an invalidation starts a newer build', async () => {
        const oldRoot = deferred<{ folders: [] }>();
        const newRoot = deferred<{ folders: [] }>();
        let rootCalls = 0;
        getFolderContentsMock.mockImplementation((parentId: string) => {
            if (parentId !== '') return Promise.resolve({ folders: [] });
            rootCalls += 1;
            return rootCalls === 1 ? oldRoot.promise : newRoot.promise;
        });

        const oldBuild = refreshFolderIndex();
        invalidateFolderIndex(1);
        const newBuild = refreshFolderIndex();
        expect(state.folderIndexBuildPromise).toBe(newBuild);

        newRoot.resolve({ folders: [] });
        const freshIndex = await newBuild;
        expect(state.folderIndexCache).toBe(freshIndex);

        oldRoot.resolve({ folders: [] });
        await oldBuild;
        await Promise.resolve();
        expect(state.folderIndexCache).toBe(freshIndex);
        expect(state.folderIndexBuildPromise).toBeNull();
    });

    it('rejects malformed folder listings rather than treating them as empty', async () => {
        getFolderContentsMock.mockResolvedValue({ folders: null });

        await expect(refreshFolderIndex()).rejects.toThrow('incomplete folder listing');
        expect(state.folderIndexCache).toBeNull();
    });
});
