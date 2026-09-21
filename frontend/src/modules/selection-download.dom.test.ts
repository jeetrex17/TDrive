import { afterEach, describe, expect, it, vi } from 'vitest';
import { state } from '../state';

const transfers = vi.hoisted(() => ({
    enqueueDownload: vi.fn(),
    enqueueFolderDownload: vi.fn(),
}));
const notify = vi.hoisted(() => vi.fn());

vi.mock('./transfers', () => transfers);
vi.mock('./notifications', () => ({ notify }));

afterEach(() => {
    state.selectedItems = new Map();
    transfers.enqueueDownload.mockReset();
    transfers.enqueueFolderDownload.mockReset();
    notify.mockReset();
});

describe('bulk selection downloads', () => {
    it('queues every selected item against its captured drive and clears the mode', async () => {
        state.selectedItems = new Map([
            ['file:41', {
                type: 'file', id: 41, name: 'first.jpg', size: 400, source: 'fs',
                parentId: '', channelId: 7,
            }],
            ['folder:docs', {
                type: 'folder', id: 'docs', name: 'Docs', parentId: '', channelId: 7,
            }],
        ] as never);
        const selection = await import('./selection') as typeof import('./selection') & {
            openSelectedItemsDownload: () => void;
        };

        selection.openSelectedItemsDownload();

        expect(transfers.enqueueDownload).toHaveBeenCalledExactlyOnceWith(41, 'first.jpg', 400, 7);
        expect(transfers.enqueueFolderDownload).toHaveBeenCalledExactlyOnceWith('docs', 'Docs', 0, 7);
        expect(state.selectedItems.size).toBe(0);
    });

    it('rejects a bulk item without captured drive provenance', async () => {
        state.selectedItems = new Map([
            ['file:41', {
                type: 'file', id: 41, name: 'first.jpg', size: 400, source: 'fs',
                parentId: '',
            }],
        ] as never);
        const { openSelectedItemsDownload } = await import('./selection');

        openSelectedItemsDownload();

        expect(transfers.enqueueDownload).not.toHaveBeenCalled();
        expect(notify).toHaveBeenCalledWith(expect.objectContaining({ level: 'error' }));
        expect(state.selectedItems.size).toBe(1);
    });

    it('keeps oversized batches selected instead of filling the queue', async () => {
        state.selectedItems = new Map(Array.from({ length: 501 }, (_, index) => [
            `file:${index + 1}`,
            {
                type: 'file', id: index + 1, name: `${index + 1}.jpg`, size: 1, source: 'fs',
                parentId: '', channelId: 7,
            },
        ])) as never;
        const { openSelectedItemsDownload } = await import('./selection');

        openSelectedItemsDownload();

        expect(transfers.enqueueDownload).not.toHaveBeenCalled();
        expect(notify).toHaveBeenCalledWith(expect.objectContaining({ level: 'warning' }));
        expect(state.selectedItems.size).toBe(501);
    });
});
