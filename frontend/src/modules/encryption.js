// Per-upload encryption support. The user picks "Encrypt before upload"
// per batch. One encryption password protects all encrypted personal
// files and is remembered only for the current app session.

import { state } from '../state.js';
import { EncryptionStatus } from '../../wailsjs/go/main/App';
import { openEncryptionPasswordModal } from './modals/encryption-password.js';

export async function loadEncryptionStatus() {
    try {
        const s = await EncryptionStatus();
        state.encryption = {
            available: !!s?.available,
            passwordSet: !!s?.password_set,
            passwordRemembered: !!s?.password_remembered,
            hint: String(s?.hint || ''),
            loaded: true,
        };
    } catch (err) {
        console.warn('EncryptionStatus failed:', err);
        state.encryption = { available: false, passwordSet: false, passwordRemembered: false, hint: '', loaded: true };
    }
    try {
        const { renderEncryptionSettingsEntry } = await import('./profile-menu.js');
        renderEncryptionSettingsEntry();
    } catch {
    }
}

// requireEncryptionPassword is the gate used by download/preview when an
// encrypted file is touched. Resolves to true on success, false on cancel.
export async function requireEncryptionPassword() {
    if (state.encryption?.passwordRemembered) return true;
    return openEncryptionPasswordModal();
}
