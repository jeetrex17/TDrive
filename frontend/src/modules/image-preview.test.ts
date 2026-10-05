import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { FileListFileRow, FolderListRow } from '../ui/file-list/types';

const preview = vi.hoisted(() => ({
    activatePreviewModal: vi.fn(),
    openPreviewList: vi.fn(async () => {}),
}));
const listed = vi.hoisted(() => ({ rows: [] as unknown[] }));

vi.mock('./modals/preview', () => preview);
vi.mock('../ui/file-list/file-list-store', () => ({ getInteractiveFileListRows: () => listed.rows }));

import { openImagePreview } from './image-preview';

function fileRow(id: string, name: string): FileListFileRow {
    return {
        kind: 'file',
        key: `file:fs:${id}`,
        selectionKey: `file:${id}`,
        id,
        name,
        baseName: name,
        ext: 'FILE',
        source: 'fs',
        parentId: '',
        channelId: 5,
        size: 100,
        metaLabel: 'Today',
        sizeLabel: '100 B',
        ariaLabel: `File: ${name}`,
        uploaderID: 7,
        uploadTime: 1_700_000_000,
        encrypted: false,
        canDelete: true,
        canRename: true,
        actions: [],
    };
}

function folderRow(id: string, name: string): FolderListRow {
    return {
        kind: 'folder',
        key: `folder:${id}`,
        selectionKey: `folder:${id}`,
        id,
        name,
        channelId: 5,
        parentId: '',
        metaLabel: '',
        sizeLabel: '',
        size: 0,
        modifiedTime: 0,
        ariaLabel: `Folder: ${name}`,
        actions: [],
    };
}

describe('openImagePreview', () => {
    beforeEach(() => {
        preview.activatePreviewModal.mockClear();
        preview.openPreviewList.mockClear();
    });

    it('opens the chosen image among the folder\'s other images, in list order', async () => {
        const chosen = fileRow('12', 'b.png');
        listed.rows = [
            folderRow('d:1', 'Trips'),
            fileRow('11', 'a.jpg'),
            fileRow('20', 'notes.txt'),
            chosen,
            fileRow('13', 'clip.mp4'),
            fileRow('14', 'c.webp'),
        ];

        await openImagePreview(chosen);

        expect(preview.activatePreviewModal).toHaveBeenCalledTimes(1);
        expect(preview.openPreviewList).toHaveBeenCalledTimes(1);
        const [images, index] = preview.openPreviewList.mock.calls[0] as unknown as [Array<{ id: number; name: string }>, number];
        expect(images.map((image) => image.name)).toEqual(['a.jpg', 'b.png', 'c.webp']);
        expect(images.map((image) => image.id)).toEqual([11, 12, 14]);
        expect(index).toBe(1);
    });

    it('starts at the first image when the row is no longer listed', async () => {
        listed.rows = [fileRow('11', 'a.jpg'), fileRow('14', 'c.webp')];

        await openImagePreview(fileRow('99', 'gone.png'));

        const [, index] = preview.openPreviewList.mock.calls[0] as unknown as [unknown[], number];
        expect(index).toBe(0);
    });
});
