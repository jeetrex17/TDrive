import { createModalController } from './modal-store';

export interface UploadOptionsPayload {
    count: number;
    queued?: boolean;
    personal?: boolean;
    destination?: string;
    names?: readonly string[];
}

export const uploadOptionsModal = createModalController<UploadOptionsPayload>();
