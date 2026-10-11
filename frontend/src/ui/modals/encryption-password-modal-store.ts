import { createModalController } from './modal-store';
import type { BiometricStatusView } from '../../types';

export interface EncryptionPasswordPayload {
    hint: string;
    biometric?: BiometricStatusView;
    biometricBusy?: boolean;
}

export const encryptionPasswordModal = createModalController<EncryptionPasswordPayload>();
