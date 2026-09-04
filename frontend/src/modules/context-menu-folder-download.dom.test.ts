import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { ContextMenuItem } from '../ui/menus/context-menu-store';

const transferMocks = vi.hoisted(() => ({
    enqueueFolderDownload: vi.fn(),
    importFolderWithParentID: vi.fn(),
    uploadWithParentID: vi.fn(),
}));

vi.mock('./transfers', () => transferMocks);
vi.mock('./selection', () => ({
    clearSelection: vi.fn(),
    ensureRowSelectedForContextMenu: vi.fn(),
    getSelectionPayload: vi.fn(() => []),
}));
vi.mock('./modals/delete', () => ({ openDeleteModal: vi.fn() }));
vi.mock('./modals/rename', () => ({ openRenameModal: vi.fn() }));
vi.mock('./modals/move', () => ({ openMoveModal: vi.fn() }));
vi.mock('./navigation', () => ({ navigateToFolder: vi.fn() }));
vi.mock('./media-types', () => ({ isVideoFile: vi.fn(() => false) }));
vi.mock('./modals/file-viewer', () => ({
    canOpenFileViewer: vi.fn(() => false),
    openFileViewer: vi.fn(),
}));
vi.mock('../ui/menus/ContextMenu.svelte', () => ({ default: {} }));
vi.mock('../ui/menus/context-menu-store', () => ({
    hideContextMenu: vi.fn(),
    showContextMenu: vi.fn(),
}));
vi.mock('../ui', () => ({ mountSvelte: vi.fn() }));

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
