// Per-upload encryption support. There is no drive-wide "encryption
// mode" — the user picks "encrypt and upload" at upload time, and the
// vault is created lazily the first time they do.
//
// This module owns the small state mirror that the UI consults
// (`vaultExists`, `unlocked`) and the helpers that gate download/preview
// when an existing encrypted file is touched.

import { state } from '../state.js';
import { EncryptionStatus } from '../../wailsjs/go/main/App';
import { openEncryptionUnlockModal } from './modals/encryption-unlock.js';

export async function loadEncryptionStatus() {
    try {
        const s = await EncryptionStatus();
        state.encryption = {
            available: !!s?.available,
            vaultExists: !!s?.vault_exists,
            unlocked: !!s?.unlocked,
            loaded: true,
        };
    } catch (err) {
        console.warn('EncryptionStatus failed:', err);
        state.encryption = { available: false, vaultExists: false, unlocked: false, loaded: true };
    }
}

// requireUnlockedForFile is the gate used by download/preview when the
// file row has encrypted=true. Resolves to true on success, false on
// cancel.
export async function requireUnlockedForFile() {
    if (state.encryption?.unlocked) return true;
    return openEncryptionUnlockModal();
}
