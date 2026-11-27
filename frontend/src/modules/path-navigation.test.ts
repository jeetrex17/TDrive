import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const api = vi.hoisted(() => ({ getFolderContents: vi.fn(), getFileList: vi.fn() }));
const refreshFiles = vi.hoisted(() => vi.fn());
const history = vi.hoisted(() => ({ steps: 0, interrupt: vi.fn() }));
vi.mock('./folder-history', () => ({
    folderHistorySteps: () => history.steps,
    interruptFolderHistoryStep: history.interrupt,
}));
vi.mock('../api', () => api);
vi.mock('../ui/channels/channel-store', () => ({ closeChannel: vi.fn() }));
vi.mock('./app-actions', () => ({ appActions: () => ({ refreshFiles }) }));
vi.mock('./navigation', () => ({ renderBreadcrumb: vi.fn() }));
vi.mock('./search', () => ({ clearSearch: vi.fn() }));

import { state } from '../state';
import { forgetPendingFileFocus, goToDrivePath } from './path-navigation';

const listings: Record<string, { folders: { id: string; name: string }[]; files: { msgId: number; name: string }[] }> = {
    '': { folders: [{ id: 'd:team', name: 'Team' }], files: [] },
    'd:team': { folders: [], files: [{ msgId: 4, name: 'plan.pdf' }] },
};

beforeEach(() => {
    api.getFolderContents.mockReset();
    api.getFileList.mockReset();
    refreshFiles.mockReset();
    history.steps = 0;
    history.interrupt.mockReset();
    api.getFolderContents.mockImplementation(async (id: string) => listings[id] ?? { folders: [], files: [] });
    api.getFileList.mockResolvedValue([]);
    state.activeChannel = { id: 1, title: 'Personal' } as never;
    state.channelSwitchInProgress = false;
    state.currentFolderId = '';
    state.folderPath = [];
    state.virtualView = null;
    state.searchQuery = '';
    state.pendingFocus = null;
});

afterEach(() => {
    vi.restoreAllMocks();
    state.activeChannel = null as never;
    state.pendingFocus = null;
});

function gate() {
    let release!: () => void;
    api.getFolderContents.mockImplementationOnce(() => new Promise((resolve) => {
        release = () => resolve(listings['']);
    }));
    return () => release();
}

describe('goToDrivePath', () => {
    it('opens a file path at its folder with the file waiting for focus', async () => {
        await expect(goToDrivePath('/team/PLAN.pdf')).resolves.toBeNull();
        expect(state.currentFolderId).toBe('d:team');
        expect(state.folderPath).toEqual([{ id: 'd:team', name: 'Team' }]);
        expect(state.pendingFocus).toEqual({ type: 'file', id: '4' });
        expect(refreshFiles).toHaveBeenCalledTimes(1);
        // A Back asked for earlier and still resolving gives way to the walk.
        expect(history.interrupt).toHaveBeenCalledTimes(1);
    });

    it('stops an earlier Back before the first read of the walk', async () => {
        const release = gate();
        const pending = goToDrivePath('/Team');
        expect(history.interrupt).toHaveBeenCalledTimes(1);
        expect(history.interrupt.mock.invocationCallOrder[0])
            .toBeLessThan(api.getFolderContents.mock.invocationCallOrder[0]);
        release();
        await expect(pending).resolves.toBeNull();
        expect(state.currentFolderId).toBe('d:team');
    });

    it('leaves the list alone when another folder opens while the path is read', async () => {
        const release = gate();
        const pending = goToDrivePath('/Team');
        // Back, Forward or a click took the list somewhere else meanwhile.
        state.currentFolderId = 'd:elsewhere';
        release();
        await expect(pending).resolves.toBeNull();
        expect(state.currentFolderId).toBe('d:elsewhere');
        expect(refreshFiles).not.toHaveBeenCalled();
    });

    it('gives way to a Back or Forward asked for while the path is read', async () => {
        const release = gate();
        const pending = goToDrivePath('/Team');
        // The step has not landed yet: the folder is still the same one.
        history.steps += 1;
        release();
        await expect(pending).resolves.toBeNull();
        expect(state.currentFolderId).toBe('');
        expect(refreshFiles).not.toHaveBeenCalled();
    });

    it('leaves Back alone for a path that cannot be read as one', async () => {
        await expect(goToDrivePath('C:\\Users')).resolves.toMatch(/on this computer/);
        state.channelSwitchInProgress = true;
        await expect(goToDrivePath('/Team')).resolves.toBe('The drive is still opening. Try again in a moment.');
        expect(history.interrupt).not.toHaveBeenCalled();
    });

    it('still answers a walk the drive cut short', async () => {
        const release = gate();
        const pending = goToDrivePath('/Team');
        state.channelSwitchInProgress = true;
        release();
        await expect(pending).resolves.toBe('The drive was busy while the path was opening. Try again.');
        expect(refreshFiles).not.toHaveBeenCalled();
    });

    it('reports a failed read of the raw root files instead of a missing file', async () => {
        vi.spyOn(console, 'error').mockImplementation(() => undefined);
        api.getFileList.mockRejectedValueOnce(new Error('offline'));
        await expect(goToDrivePath('/voice.ogg')).resolves.toBe('Could not read the drive. Try again.');
        expect(refreshFiles).not.toHaveBeenCalled();
    });
});

describe('forgetPendingFileFocus', () => {
    it('drops a file still waiting for its row', () => {
        state.pendingFocus = { type: 'file', id: '4' } as never;
        forgetPendingFileFocus();
        expect(state.pendingFocus).toBeNull();
    });

    it('keeps a folder waiting for focus', () => {
        state.pendingFocus = { type: 'folder', id: 'd:team' } as never;
        forgetPendingFileFocus();
        expect(state.pendingFocus).toEqual({ type: 'folder', id: 'd:team' });
    });
});
