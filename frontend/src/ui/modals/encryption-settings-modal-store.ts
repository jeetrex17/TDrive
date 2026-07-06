import { createModalController } from './modal-store';

export interface EncryptionSettingsPayload {
    hint: string;
}

export const encryptionSettingsModal = createModalController<EncryptionSettingsPayload>();
