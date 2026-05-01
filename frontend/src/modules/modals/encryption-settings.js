// Encryption settings: safely change the encryption password and hint.
// There is intentionally no "forgot password" reset; without the current
// password encrypted files cannot be recovered.

import { ChangeEncryptionPassword } from '../../../wailsjs/go/main/App';
import { state } from '../../state.js';
import { loadEncryptionStatus } from '../encryption.js';
import { notify } from '../notifications.js';

export function setupEncryptionSettingsModal() {
    const modal = document.getElementById('encryption-settings-modal');
    if (!modal) return;
    const cancel = modal.querySelector('#encryption-settings-cancel');
    const confirm = modal.querySelector('#encryption-settings-confirm');
    const current = modal.querySelector('#encryption-current-password');
    const next = modal.querySelector('#encryption-new-password');
    const next2 = modal.querySelector('#encryption-new-password-confirm');
    const hint = modal.querySelector('#encryption-settings-hint');
    const errEl = modal.querySelector('#encryption-settings-error');

    const showError = (msg) => {
        if (!errEl) return;
        errEl.textContent = String(msg);
        errEl.style.display = 'block';
    };
    const clearError = () => {
        if (!errEl) return;
        errEl.textContent = '';
        errEl.style.display = 'none';
    };
    const close = () => {
        modal.style.display = 'none';
        if (current) current.value = '';
        if (next) next.value = '';
        if (next2) next2.value = '';
        clearError();
    };

    cancel.addEventListener('click', close);
    modal.addEventListener('click', (e) => { if (e.target === modal) close(); });

    confirm.addEventListener('click', async () => {
        const oldPassword = String(current?.value || '');
        const newPassword = String(next?.value || '');
        const confirmPassword = String(next2?.value || '');

        clearError();
        if (!oldPassword) { showError('Enter your current password.'); return; }
        if (newPassword.length < 8) { showError('Use at least 8 characters for the new password.'); return; }
        if (newPassword !== confirmPassword) { showError('New passwords do not match.'); return; }

        confirm.disabled = true;
        cancel.disabled = true;
        try {
            await ChangeEncryptionPassword(oldPassword, newPassword, String(hint?.value || ''));
            await loadEncryptionStatus();
            close();
            notify({
                level: 'success',
                title: 'Encryption password changed',
                body: 'Use the new password for encrypted files from now on.',
            });
        } catch (err) {
            showError(String(err));
        } finally {
            confirm.disabled = false;
            cancel.disabled = false;
        }
    });
}

export function openEncryptionSettingsModal() {
    const modal = document.getElementById('encryption-settings-modal');
    if (!modal) return;
    const hint = modal.querySelector('#encryption-settings-hint');
    if (hint) hint.value = String(state.encryption?.hint || '');
    modal.style.display = 'flex';
    const current = modal.querySelector('#encryption-current-password');
    setTimeout(() => current?.focus(), 0);
}
