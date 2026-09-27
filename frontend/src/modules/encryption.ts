// Per-upload encryption support. The user picks "Encrypt before upload"
// per batch. One encryption password protects all encrypted personal
// files and is remembered only for the current app session.

import { state } from '../state';
import { getEncryptionStatus } from '../api';
import { openEncryptionPasswordModal } from './modals/encryption-password';
import { openEncryptionSetupModal } from './modals/encryption-setup';
import { encryptionEntryVisible } from '../ui/chrome/profile-store';
import { isEncryptionPasswordRequired } from './errors';
import type { EncryptionStatusView } from '../types';

export async function loadEncryptionStatus(throwOnError = false): Promise<EncryptionStatusView | null> {
    const previous = state.encryption;
    let driveUnavailable = false;
    try {
        const s = await getEncryptionStatus();
        if (!s.available) {
            driveUnavailable = true;
            throw new Error('Encryption status is unavailable. Reopen My Drive and try again.');
        }
        state.encryption = {
            available: s.available,
            passwordSet: s.passwordSet,
            passwordRemembered: s.passwordRemembered,
            hint: s.hint,
            loaded: true,
        };
        renderEncryptionSettingsEntry();
        return s;
    } catch (err) {
        console.warn('EncryptionStatus failed:', err);
        state.encryption = !driveUnavailable && previous?.available && previous.passwordSet
            ? { ...previous, passwordRemembered: false, loaded: false }
            : { available: false, passwordSet: false, passwordRemembered: false, hint: '', loaded: false };
        renderEncryptionSettingsEntry();
        if (throwOnError) throw err;
        return null;
    }
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
    if (state.encryption?.passwordRemembered) return true;
    let status: EncryptionStatusView | null;
    try {
        status = await loadEncryptionStatus(true);
    } catch (err) {
        if (state.encryption?.available && state.encryption.passwordSet) {
            return openEncryptionPasswordModal() as Promise<boolean>;
        }
        throw err;
    }
    if (!status) throw new Error('Encryption status is unavailable. Reopen My Drive and try again.');
    if (status.passwordRemembered) return true;
    return status.passwordSet
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
