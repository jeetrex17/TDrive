import { createModalController } from '../modals/modal-store';

/**
 * What the trash's confirm dialog is about. It carries plain strings rather
 * than the row itself: the dialog is a confirmation, and a row that changed
 * or disappeared underneath it must not change the question being asked.
 */
export type TrashConfirmation =
    | { kind: 'purge'; objectId: string; name: string; isFolder: boolean }
    | { kind: 'empty'; summary: string };

export const trashConfirmModal = createModalController<TrashConfirmation>();
