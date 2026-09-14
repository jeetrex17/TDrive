// Password prompt for encrypted uploads, downloads, and previews.
// On success the backend remembers the decrypted master key in memory
// until the app exits, so users do not re-enter the password per file.

import { useEncryptionPassword, type OperationResult } from '../../api';
import { loadEncryptionStatus } from '../encryption';
import { humanizeBackendError } from '../errors';
import { state } from '../../state';
import { encryptionPasswordModal } from '../../ui/modals/encryption-password-modal-store';
let pending: ((ok: boolean) => void) | null = null;

function finish(ok: boolean): void {
    encryptionPasswordModal.close();
    if (pending) {
        const resolve = pending;
        pending = null;
        resolve(ok);
    }
}
export function cancelEncryptionPassword(): void {
    finish(false);
}



export async function submitEncryptionPassword(password: string): Promise<void> {
    if (!password) {
        encryptionPasswordModal.setError('Enter your encryption password.');
        return;
    }
    encryptionPasswordModal.setError('');
    encryptionPasswordModal.setBusy(true);
    try {
        const result = await useEncryptionPassword(password);
        if (!result.ok) {
            encryptionPasswordModal.setError(humanizeBackendError(result.error));
            return;
        }
        await loadEncryptionStatus();
        finish(true);
    } catch (err) {
        encryptionPasswordModal.setError(humanizeBackendError(err));
    } finally {
        encryptionPasswordModal.setBusy(false);
    }
}

// Retry exactly once when the stable backend code says a locked vault blocked
// the operation. Display wording is intentionally irrelevant to this branch.
export async function callWithPasswordRetry(call: () => Promise<OperationResult>): Promise<OperationResult> {
    let result = await call();
    if (!result.ok && result.error.code === "encryption_password_required") {
        const unlocked = await openEncryptionPasswordModal();
        if (!unlocked) {
            return {
                ok: false,
                error: { code: 'canceled', message: 'Encryption password entry was canceled' },
            };
        }
        result = await call();
    }
    return result;
}

export function openEncryptionPasswordModal(): Promise<boolean> {
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

        const showPrompt = async () => {
            // The hint lives in the synced encryption config; refresh once so
            // the prompt can show it.
            if (!state.encryption?.loaded || !state.encryption?.hint) {
                await loadEncryptionStatus();
            }
            encryptionPasswordModal.open({ hint: String(state.encryption?.hint || '') });
        };
        showPrompt().catch(() => {
            encryptionPasswordModal.open({ hint: '' });
        });
    });
}
