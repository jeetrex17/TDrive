import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

// Each index read waits until the test finishes it, oldest first.
const index = vi.hoisted(() => ({ reads: 0, waiting: [] as Array<() => void> }));
vi.mock('./folder-index', () => ({
    refreshFolderIndex: () => new Promise((resolve) => {
        index.reads += 1;
        const byId = new Map([['a', { id: 'a', name: 'Projects', parentId: '' }]]);
        index.waiting.push(() => resolve({ folders: [...byId.values()], byId, children: new Map() }));
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

function finishRead(): void {
    index.waiting.shift()?.();
}

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

afterEach(async () => {
    // A failed test must not leave a step pending for the next one.
    while (index.waiting.length) finishRead();
    await new Promise((resolve) => setTimeout(resolve, 0));
});

describe('folderHistorySteps', () => {
    it('counts a step with somewhere to go as soon as it is asked for', async () => {
        const before = folderHistorySteps();
        const back = stepFolderHistory(-1);
        expect(folderHistorySteps()).toBe(before + 1);
        expect(index.reads).toBe(1);
        // A second Back has somewhere to go, but is dropped while the first resolves.
        await expect(stepFolderHistory(-1)).resolves.toBeNull();
        expect(folderHistorySteps()).toBe(before + 1);
        expect(index.reads).toBe(1);
        finishRead();
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
        finishRead();
        await expect(back).resolves.toBeNull();
        // Still at Projects, so Back has somewhere to go next time.
        const again = stepFolderHistory(-1);
        finishRead();
        await expect(again).resolves.toEqual([]);
    });

    it('lets a Back through while an interrupted one is still reading', async () => {
        const old = stepFolderHistory(-1);
        interruptFolderHistoryStep();
        const fresh = stepFolderHistory(-1);
        expect(index.reads).toBe(2);
        finishRead();
        await expect(old).resolves.toBeNull();
        // The old step finishing must not release the newer one's hold.
        await expect(stepFolderHistory(-1)).resolves.toBeNull();
        expect(index.reads).toBe(2);
        finishRead();
        await expect(fresh).resolves.toEqual([]);
    });
});
