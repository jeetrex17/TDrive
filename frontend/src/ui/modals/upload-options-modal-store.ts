import { createModalController } from './modal-store';

export interface UploadOptionsPayload {
    count: number;
}

export const uploadOptionsModal = createModalController<UploadOptionsPayload>();
