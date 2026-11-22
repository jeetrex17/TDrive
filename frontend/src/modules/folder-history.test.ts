import { beforeEach, describe, expect, it, vi } from 'vitest';

const folders = vi.hoisted(() => new Map<string, { id: string; name: string; parentId: string }>());
vi.mock('./folder-index', () => ({
    refreshFolderIndex: vi.fn(async () => ({ folders: [...folders.values()], byId: folders, children: new Map() })),
}));

import { state } from '../state';
import { recordFolderPlace, resetFolderHistory, stepFolderHistory } from './folder-history';

function addFolder(id: string, name: string, parentId = ''): void {
    folders.set(id, { id, name, parentId });
}

function visit(...ids: string[]): void {
    state.folderPath = ids.map((id) => ({ id, name: folders.get(id)?.name ?? id }));
    state.currentFolderId = ids[ids.length - 1] ?? '';
    recordFolderPlace();
}

/** Steps like navigateHistory does: the shown path becomes the current place. */
async function step(delta: -1 | 1): Promise<string[] | null> {
    const path = await stepFolderHistory(delta);
    if (path) visit(...path.map((entry) => entry.id));
    return path ? path.map((entry) => entry.name) : null;
}

beforeEach(() => {
    folders.clear();
    addFolder('a', 'Projects');
    addFolder('b', 'Specs', 'a');
    addFolder('c', 'Photos');
    state.activeChannel = { id: 7, title: 'My Drive', kind: 'personal' };
    state.virtualView = null;
    resetFolderHistory();
    visit();
});

describe('folder history', () => {
    it('goes back and forward, and a new visit drops the forward entries', async () => {
        visit('a');
        visit('a', 'b');
        expect(await step(-1)).toEqual(['Projects']);
        expect(await step(-1)).toEqual([]);
        expect(await step(-1)).toBeNull();
        expect(await step(1)).toEqual(['Projects']);

        visit('c');
        expect(await step(1)).toBeNull();
        expect(await step(-1)).toEqual(['Projects']);
    });

    it('follows renames and moves, and skips folders that were deleted', async () => {
        visit('a');
        visit('c');
        visit('a', 'b');
        folders.delete('c');
        addFolder('b', 'Specs v2', 'c2');
        addFolder('c2', 'Archive');

        expect(await step(-1)).toEqual(['Projects']);
        expect(await step(1)).toEqual(['Archive', 'Specs v2']);
    });

    it('starts over in another drive and ignores Media, Trash and half-resolved paths', async () => {
        visit('a');
        state.virtualView = 'trash';
        visit('c');
        state.virtualView = null;
        state.folderPath = [];
        state.currentFolderId = 'b';
        recordFolderPlace();
        expect(await step(-1)).toEqual([]);

        state.activeChannel = { id: 8, title: 'Shared', kind: 'shared' };
        visit();
        visit('c');
        expect(await step(-1)).toEqual([]);
        expect(await step(-1)).toBeNull();
    });

    it('starts at the drive root when the first recorded place is a subfolder', async () => {
        resetFolderHistory();
        visit('a', 'b');
        expect(await step(-1)).toEqual([]);
    });

    it('drops a second press while a step is still resolving', async () => {
        visit('a');
        visit('a', 'b');
        const first = stepFolderHistory(-1);
        expect(await stepFolderHistory(-1)).toBeNull();
        expect((await first)?.map((entry) => entry.name)).toEqual(['Projects']);
    });
});
