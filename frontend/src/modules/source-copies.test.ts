import { get } from 'svelte/store';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { SourceCopyResult } from '../api';

const api = vi.hoisted(() => ({
    getMyUserId: vi.fn(),
    listSourceCopies: vi.fn(),
    listSourceCopyBatch: vi.fn(),
    listSourceCopyFolders: vi.fn(),
    previewSourceCopyNames: vi.fn(),
    saveSourceCopies: vi.fn(),
    retrySourceCopy: vi.fn(),
}));

vi.mock('../api', () => api);
vi.mock('../state', () => ({ invalidateFolderIndex: vi.fn(), state: { channels: [] } }));
vi.mock('./channels', () => ({ switchActiveChannel: vi.fn() }));
vi.mock('./navigation', () => ({ renderBreadcrumb: vi.fn() }));
vi.mock('./notifications', () => ({ notify: vi.fn(), notifyAppError: vi.fn() }));

import {
    activateSourceCopies,
    checkSourceCopy,
    clearSourceCopyHistory,
    confirmSourceCopy,
    openSharedDriveCopy,
    sourceCopyActivity,
    sourceCopyDialog,
} from './source-copies';

const pending: SourceCopyResult = {
    id: 'existing-copy', name: 'interrupted.zip', parentId: '',
    status: 'pending', fileId: 0, message: 'Waiting for a receipt',
};
const saved: SourceCopyResult = {
    id: 'current-copy', name: 'notes.pdf', parentId: '',
    status: 'saved', fileId: 400, message: '',
};

function deferredReceipts() {
    let resolve!: (results: SourceCopyResult[]) => void;
    const promise = new Promise<SourceCopyResult[]>((finish) => { resolve = finish; });
    return { promise, resolve };
}

describe('copy Activity reconciliation', () => {
    let deactivate = () => {};

    beforeEach(() => {
        vi.resetAllMocks();
        api.getMyUserId.mockResolvedValue(7);
        api.listSourceCopies.mockResolvedValue([]);
        api.listSourceCopyFolders.mockResolvedValue({ driveId: 1, folders: [] });
        api.previewSourceCopyNames.mockResolvedValue([saved.name]);
        api.saveSourceCopies.mockResolvedValue({ results: [saved], notStarted: '' });
    });

    afterEach(() => deactivate());

    async function openCopy(): Promise<void> {
        deactivate = activateSourceCopies();
        openSharedDriveCopy(7, { id: 2, title: 'Shared' }, [
            { id: 42, name: saved.name, revision: 1, identity: 'managed' },
        ]);
        await vi.waitFor(() => expect(get(sourceCopyDialog)?.stage).toBe('ready'));
    }

    it('keeps other pending receipts when startup finishes after a successful save', async () => {
        const startup = deferredReceipts();
        api.listSourceCopies.mockReturnValueOnce(startup.promise);
        await openCopy();
        await confirmSourceCopy();
        startup.resolve([pending]);

        await vi.waitFor(() => expect(get(sourceCopyActivity)).toEqual([pending]));
    });

    it('does not restore a pending receipt that settled after startup began', async () => {
        const startup = deferredReceipts();
        api.listSourceCopies.mockReturnValueOnce(startup.promise);
        api.saveSourceCopies.mockResolvedValueOnce({ results: [pending], notStarted: '' });
        api.retrySourceCopy.mockResolvedValueOnce({ ...pending, status: 'saved' });
        await openCopy();
        await confirmSourceCopy();
        await checkSourceCopy(pending.id);

        startup.resolve([pending, { ...pending, id: 'untouched' }]);
        await vi.waitFor(() => expect(get(sourceCopyActivity).map(({ id }) => id)).toEqual(['untouched']));
    });

    it('recovers only the attempted batch after the save response is lost', async () => {
        api.saveSourceCopies.mockRejectedValueOnce(new Error('Lost save response'));
        api.listSourceCopyBatch.mockResolvedValueOnce([saved]);
        await openCopy();
        await confirmSourceCopy();

        const batchId = api.saveSourceCopies.mock.lastCall?.[5];
        expect(typeof batchId).toBe('string');
        expect(batchId).toMatch(/^[a-f0-9-]{36}$/);
        expect(api.listSourceCopyBatch).toHaveBeenCalledExactlyOnceWith(7, 1, batchId);
        expect(api.listSourceCopies).toHaveBeenCalledTimes(1);
        await vi.waitFor(() => expect(get(sourceCopyActivity)).toEqual([saved]));
    });

    it('keeps the picker editable after a confirmed preflight failure', async () => {
        api.saveSourceCopies.mockResolvedValueOnce({ results: [], notStarted: 'The folder disappeared.' });
        await openCopy();
        await confirmSourceCopy();

        expect(get(sourceCopyDialog)?.stage).toBe('ready');
        expect(get(sourceCopyDialog)?.error).toBeTruthy();
        expect(api.listSourceCopyBatch).not.toHaveBeenCalled();
        expect(get(sourceCopyActivity)).toEqual([]);
    });

    it('keeps a history clear effective while recovery is in flight', async () => {
        const batch = deferredReceipts();
        api.saveSourceCopies.mockRejectedValueOnce(new Error('Lost save response'));
        api.listSourceCopyBatch.mockReturnValueOnce(batch.promise);
        await openCopy();
        await confirmSourceCopy();
        clearSourceCopyHistory();
        batch.resolve([saved, pending]);

        await vi.waitFor(() => expect(get(sourceCopyActivity)).toEqual([pending]));
    });
});
