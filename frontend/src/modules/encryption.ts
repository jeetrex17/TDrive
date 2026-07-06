// Per-upload encryption support. The user picks "Encrypt before upload"
// per batch. One encryption password protects all encrypted personal
// files and is remembered only for the current app session.

import { state } from '../state';
import { EncryptionStatus } from '../../wailsjs/go/main/App';
import { openEncryptionPasswordModal } from './modals/encryption-password';
import { encryptionEntryVisible } from '../ui/chrome/profile-store';

export async function loadEncryptionStatus(): Promise<void> {
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
    renderEncryptionSettingsEntry();
}

// renderEncryptionSettingsEntry lives here (not in profile-menu.ts) so this
// module never imports the profile menu, whose modal imports circle back to
// loadEncryptionStatus.
export function renderEncryptionSettingsEntry() {
    encryptionEntryVisible.set(Boolean(state.encryption?.passwordSet));
}

// requireEncryptionPassword is the gate used by download/preview when an
// encrypted file is touched. Resolves to true on success, false on cancel.
export async function requireEncryptionPassword(): Promise<boolean> {
    if (state.encryption?.passwordRemembered) return true;
    return openEncryptionPasswordModal() as Promise<boolean>;
}
