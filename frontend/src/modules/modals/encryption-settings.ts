// Encryption settings: safely change the encryption password and hint.
// There is intentionally no "forgot password" reset; without the current
// password encrypted files cannot be recovered.

import { ChangeEncryptionPassword } from '../../../wailsjs/go/main/App';
import { state } from '../../state';
import { loadEncryptionStatus } from '../encryption';
import { notify } from '../notifications';
import EncryptionSettingsModal from '../../ui/modals/EncryptionSettingsModal.svelte';
import { encryptionSettingsModal } from '../../ui/modals/encryption-settings-modal-store';
import { mountSvelte, type SvelteMountHandle } from '../../ui/mount';

let encryptionSettingsModalHandle: SvelteMountHandle<Record<string, unknown>> | null = null;

export function setupEncryptionSettingsModal() {
    const modal = document.getElementById('encryption-settings-modal');
    if (!modal || encryptionSettingsModalHandle) return;

    modal.replaceChildren();
    encryptionSettingsModalHandle = mountSvelte(EncryptionSettingsModal, {
        target: modal,
        props: {
            onCancel: () => encryptionSettingsModal.close(),
            onSubmit: submitChange,
        },
    });
}

export function openEncryptionSettingsModal() {
    encryptionSettingsModal.open({ hint: String(state.encryption?.hint || '') });
}

async function submitChange(
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
        await ChangeEncryptionPassword(currentPassword, newPassword, hint);
        await loadEncryptionStatus();
        encryptionSettingsModal.close();
        notify({
            level: 'success',
            title: 'Encryption password changed',
            body: 'Use the new password for encrypted files from now on.',
        });
    } catch (err) {
        encryptionSettingsModal.setError(String(err));
    } finally {
        encryptionSettingsModal.setBusy(false);
    }
}
