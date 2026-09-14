// Encryption settings: safely change the encryption password and hint.
// There is intentionally no "forgot password" reset; without the current
// password encrypted files cannot be recovered.

import { changeEncryptionPassword } from '../../api';
import { state } from '../../state';
import { loadEncryptionStatus } from '../encryption';
import { notify } from '../notifications';
import { humanizeBackendError } from '../errors';
import { encryptionSettingsModal } from '../../ui/modals/encryption-settings-modal-store';
export function cancelEncryptionSettings(): void {
    encryptionSettingsModal.close();
}



export function openEncryptionSettingsModal() {
    encryptionSettingsModal.open({ hint: String(state.encryption?.hint || '') });
}

export async function submitEncryptionSettings(
    currentPassword: string,
    newPassword: string,
    confirmPassword: string,
    hint: string,
): Promise<void> {
    encryptionSettingsModal.setError('');
    if (!currentPassword) {
        encryptionSettingsModal.setError('Enter your current password.');
        return;
    }
    if (newPassword.length < 8) {
        encryptionSettingsModal.setError('Use at least 8 characters for the new password.');
        return;
    }
    if (newPassword !== confirmPassword) {
        encryptionSettingsModal.setError('New passwords do not match.');
        return;
    }

    encryptionSettingsModal.setBusy(true);
    try {
        const result = await changeEncryptionPassword(currentPassword, newPassword, hint);
        if (!result.ok) {
            encryptionSettingsModal.setError(humanizeBackendError(result.error));
            return;
        }
        await loadEncryptionStatus();
        encryptionSettingsModal.close();
        notify({
            level: 'success',
            title: 'Encryption password changed',
            body: 'Use the new password for encrypted files from now on.',
        });
    } catch (err) {
        encryptionSettingsModal.setError(humanizeBackendError(err));
    } finally {
        encryptionSettingsModal.setBusy(false);
    }
}
