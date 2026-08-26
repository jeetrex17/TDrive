import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { ContextMenuItem } from '../ui/menus/context-menu-store';

const transferMocks = vi.hoisted(() => ({
    enqueueFolderDownload: vi.fn(),
    importFolderWithParentID: vi.fn(),
    uploadWithParentID: vi.fn(),
}));

vi.mock('./transfers', () => transferMocks);

beforeEach(() => {
    vi.clearAllMocks();
});

describe('folder context menu download', () => {
    it('places an exact Download action immediately after Open and preserves the remaining actions', async () => {
        const { buildFolderContextMenuItems } = await import('./context-menu');
        const items = buildFolderContextMenuItems('d:screenshots', 'Screenshots');
        const labels = items
            .filter((item): item is Extract<ContextMenuItem, { label: string }> => item.type !== 'divider')
            .map((item) => item.label);

        expect(labels).toEqual([
            'Open "Screenshots"',
            'Download "Screenshots"',
            'Upload files to this folder',
            'Upload folder to this folder',
            'Rename…',
            'Move to…',
            'Delete "Screenshots"',
            'New folder',
            'Refresh',
        ]);

        const download = items.find((item): item is Extract<ContextMenuItem, { label: string }> =>
            item.type !== 'divider' && item.label === 'Download "Screenshots"');
        if (!download) throw new Error('download menu item missing');
        download.action();
        expect(transferMocks.enqueueFolderDownload).toHaveBeenCalledWith('d:screenshots', 'Screenshots');
    });
});
