import { describe, expect, it, vi } from 'vitest';

let finishIndex: () => void = () => undefined;
vi.mock('./folder-index', () => ({
    refreshFolderIndex: () => new Promise((resolve) => {
        finishIndex = () => resolve({ byId: new Map() });
    }),
}));

import { folderHistorySteps, stepFolderHistory } from './folder-history';

describe('folderHistorySteps', () => {
    it('counts a step as soon as it is asked for, and not one dropped while another resolves', async () => {
        const before = folderHistorySteps();
        const first = stepFolderHistory(-1);
        expect(folderHistorySteps()).toBe(before + 1);
        // Dropped: the first step is still resolving.
        await expect(stepFolderHistory(1)).resolves.toBeNull();
        expect(folderHistorySteps()).toBe(before + 1);
        finishIndex();
        await first;
    });
});
