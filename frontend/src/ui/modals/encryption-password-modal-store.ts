import { createModalController } from './modal-store';

export interface EncryptionPasswordPayload {
    hint: string;
}

export const encryptionPasswordModal = createModalController<EncryptionPasswordPayload>();
