// Opening an image from the file list, whichever way it was asked for: Enter,
// a double click, the row's Open button or the menu's Open item.

import { getInteractiveFileListRows } from '../ui/file-list/file-list-store';
import type { FileListFileRow } from '../ui/file-list/types';
import { isImageFile } from './media-types';

/**
 * Previews an image in the context of its folder, so next and previous move
 * through the other images listed there, in list order.
 */
export async function openImagePreview(row: FileListFileRow): Promise<void> {
    const images = getInteractiveFileListRows()
        .filter((candidate): candidate is FileListFileRow => candidate.kind === 'file' && isImageFile(candidate.name))
        .map((image) => ({
            type: 'file',
            id: Number(image.id),
            name: image.name,
            size: image.size,
            encrypted: image.encrypted,
            uploaderId: image.uploaderID,
            uploadTime: image.uploadTime,
        }));
    const index = Math.max(0, images.findIndex((image) => String(image.id) === row.id));
    const preview = await import('./modals/preview');
    preview.activatePreviewModal();
    await preview.openPreviewList(images, index);
}
