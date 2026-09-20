// Per-upload encryption support. The user picks "Encrypt before upload"
// per batch. One encryption password protects all encrypted personal
// files and is remembered only for the current app session.

import { state } from '../state';
import { getEncryptionStatus } from '../api';
import { openEncryptionPasswordModal } from './modals/encryption-password';
import { openEncryptionSetupModal } from './modals/encryption-setup';
import { encryptionEntryVisible } from '../ui/chrome/profile-store';
import { isEncryptionPasswordRequired } from './errors';

export async function loadEncryptionStatus(): Promise<void> {
    try {
        const s = await getEncryptionStatus();
        state.encryption = {
            available: s.available,
            passwordSet: s.passwordSet,
            passwordRemembered: s.passwordRemembered,
            hint: s.hint,
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

// requireEncryptionPassword gates encrypted file access and encrypted mounts.
// It resolves to true on success and false when the user cancels.
export async function requireEncryptionPassword(): Promise<boolean> {
    if (!state.encryption?.loaded) await loadEncryptionStatus();
    if (state.encryption?.passwordRemembered) return true;
    return state.encryption?.passwordSet
        ? openEncryptionPasswordModal() as Promise<boolean>
        : openEncryptionSetupModal();
}

// Opens a protected resource after one password prompt. The metadata hint
// avoids a doomed backend call for known encrypted files; the error fallback
// covers stale or incomplete list metadata returned by older projections.
export async function accessEncryptedResource<T>(
    encrypted: boolean,
    open: () => Promise<T>,
): Promise<T | null> {
    if (encrypted && !await requireEncryptionPassword()) return null;
    try {
        return await open();
    } catch (error) {
        if (!isEncryptionPasswordRequired(error)) throw error;
        if (!await openEncryptionPasswordModal()) return null;
        return open();
    }
}
