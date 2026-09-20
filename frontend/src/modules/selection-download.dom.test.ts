import { afterEach, describe, expect, it, vi } from 'vitest';
import { state } from '../state';

const transfers = vi.hoisted(() => ({
    enqueueDownload: vi.fn(),
    enqueueFolderDownload: vi.fn(),
}));

vi.mock('./transfers', () => transfers);

afterEach(() => {
    state.selectedItems = new Map();
    transfers.enqueueDownload.mockReset();
    transfers.enqueueFolderDownload.mockReset();
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
});
