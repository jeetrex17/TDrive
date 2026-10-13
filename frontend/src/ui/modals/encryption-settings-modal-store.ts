import { createModalController } from './modal-store';
import type { BiometricStatusView } from '../../types';

export interface EncryptionSettingsPayload {
    hint: string;
    biometric?: BiometricStatusView;
    biometricBusy?: boolean;
}

export const encryptionSettingsModal = createModalController<EncryptionSettingsPayload>();
