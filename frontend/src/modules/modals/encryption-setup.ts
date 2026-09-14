// First-time encryption password setup. Triggered when a user first
// chooses "Encrypt before upload". This password protects every
// encrypted personal file; if forgotten, those files cannot be recovered.

import { createEncryptionPassword } from '../../api';
import { notify } from '../notifications';
import { loadEncryptionStatus } from '../encryption';
import { humanizeBackendError } from '../errors';
import { encryptionSetupModal } from '../../ui/modals/encryption-setup-modal-store';
let pending: ((ok: boolean) => void) | null = null;

function finish(ok: boolean): void {
    encryptionSetupModal.close();
    if (pending) {
        const resolve = pending;
        pending = null;
        resolve(ok);
    }
}
export function cancelEncryptionSetup(): void {
    finish(false);
}



export async function submitEncryptionSetup(password: string, confirmPassword: string, hint: string): Promise<void> {
    if (password.length < 8) {
        encryptionSetupModal.setError('Use at least 8 characters.');
        return;
    }
    if (password !== confirmPassword) {
        encryptionSetupModal.setError('Passwords don’t match.');
        return;
    }

    encryptionSetupModal.setError('');
    encryptionSetupModal.setBusy(true);
    try {
        const result = await createEncryptionPassword(password, hint);
        if (!result.ok) {
            encryptionSetupModal.setError(humanizeBackendError(result.error));
            return;
        }
        await loadEncryptionStatus();
        finish(true);
        notify({
            level: 'success',
            title: 'Encryption password created',
            body: 'Encrypted uploads will be protected before they leave this device.',
        });
    } catch (err) {
        encryptionSetupModal.setError(humanizeBackendError(err));
    } finally {
        encryptionSetupModal.setBusy(false);
    }
}

export function openEncryptionSetupModal(): Promise<boolean> {
    return new Promise((resolve) => {
        // A second open while one is pending keeps the visible modal and lets
        // both callers observe the same eventual outcome.
        if (pending) {
            const prev = pending;
            pending = (ok) => {
                prev(ok);
                resolve(ok);
            };
            return;
        }
        pending = resolve;
        encryptionSetupModal.open(null);
    });
}
