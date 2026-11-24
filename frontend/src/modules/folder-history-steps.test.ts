import { beforeEach, describe, expect, it, vi } from 'vitest';

const index = vi.hoisted(() => ({ reads: 0, finish: (): void => undefined }));
vi.mock('./folder-index', () => ({
    refreshFolderIndex: () => new Promise((resolve) => {
        index.reads += 1;
        const byId = new Map([['a', { id: 'a', name: 'Projects', parentId: '' }]]);
        index.finish = () => resolve({ folders: [...byId.values()], byId, children: new Map() });
    }),
}));

import { state } from '../state';
import {
    folderHistorySteps,
    interruptFolderHistoryStep,
    recordFolderPlace,
    resetFolderHistory,
    stepFolderHistory,
} from './folder-history';

beforeEach(() => {
    index.reads = 0;
    state.activeChannel = { id: 7, title: 'My Drive', kind: 'personal' };
    state.virtualView = null;
    resetFolderHistory();
    // History: the drive root, then Projects.
    state.folderPath = [{ id: 'a', name: 'Projects' }];
    state.currentFolderId = 'a';
    recordFolderPlace();
});

describe('folderHistorySteps', () => {
    it('counts a step with somewhere to go as soon as it is asked for', async () => {
        const before = folderHistorySteps();
        const back = stepFolderHistory(-1);
        expect(folderHistorySteps()).toBe(before + 1);
        expect(index.reads).toBe(1);
        // Dropped while the first step resolves: not counted.
        await expect(stepFolderHistory(1)).resolves.toBeNull();
        expect(folderHistorySteps()).toBe(before + 1);
        index.finish();
        await expect(back).resolves.toEqual([]);
    });

    it('does not count a step with nowhere to go', async () => {
        const before = folderHistorySteps();
        await expect(stepFolderHistory(1)).resolves.toBeNull();
        resetFolderHistory();
        await expect(stepFolderHistory(-1)).resolves.toBeNull();
        expect(folderHistorySteps()).toBe(before);
        expect(index.reads).toBe(0);
    });

    it('drops a step that a newer navigation interrupted while it resolved', async () => {
        const back = stepFolderHistory(-1);
        interruptFolderHistoryStep();
        index.finish();
        await expect(back).resolves.toBeNull();
        // Still at Projects, so Back has somewhere to go next time.
        const again = stepFolderHistory(-1);
        index.finish();
        await expect(again).resolves.toEqual([]);
    });
});
