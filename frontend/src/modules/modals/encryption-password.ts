// Password prompt for encrypted uploads, downloads, and previews.
// On success the backend remembers the decrypted master key in memory
// until the app exits, so users do not re-enter the password per file.

import { get } from 'svelte/store';
import { getBiometricStatus, useBiometricUnlock, useEncryptionPassword, type OperationResult } from '../../api';
import { cancelBiometricAuthentication, waitForBiometricCancellation } from '../biometric';
import { loadEncryptionStatus } from '../encryption';
import { humanizeBackendError } from '../errors';
import { state } from '../../state';
import { encryptionPasswordModal } from '../../ui/modals/encryption-password-modal-store';
interface UnlockRequest {
    promise: Promise<boolean>;
    resolve: (ok: boolean) => void;
}

let pending: UnlockRequest | null = null;

function isCurrent(request: UnlockRequest): boolean {
    return pending === request && get(encryptionPasswordModal.state).open;
}

function finish(ok: boolean): void {
    const request = pending;
    pending = null;
    encryptionPasswordModal.close();
    request?.resolve(ok);
}
export function cancelEncryptionPassword(): void {
    const nativeBusy = get(encryptionPasswordModal.state).payload?.biometricBusy;
    finish(false);
    if (nativeBusy) {
        // The native backend invalidates the key handoff before canceling its
        // prompt. UI closure remains immediate even if the bridge is delayed.
        cancelBiometricAuthentication();
    }
}

export async function submitEncryptionPassword(password: string): Promise<void> {
    const request = pending;
    if (!request || !isCurrent(request) || get(encryptionPasswordModal.state).busy) return;
    if (!password) {
        encryptionPasswordModal.setError('Enter your encryption password.');
        return;
    }
    encryptionPasswordModal.setError('');
    encryptionPasswordModal.setBusy(true);
    try {
        const result = await useEncryptionPassword(password);
        if (!isCurrent(request)) return;
        if (!result.ok) {
            encryptionPasswordModal.setError(humanizeBackendError(result.error));
            return;
        }
        await loadEncryptionStatus();
        if (!isCurrent(request)) return;
        window.dispatchEvent(new Event('tdrive:unlocked'));
        finish(true);
    } catch (err) {
        if (isCurrent(request)) encryptionPasswordModal.setError(humanizeBackendError(err));
    } finally {
        if (isCurrent(request)) encryptionPasswordModal.setBusy(false);
    }
}

export async function submitEncryptionBiometric(): Promise<void> {
    const request = pending;
    const view = get(encryptionPasswordModal.state);
    if (!request || !isCurrent(request) || view.busy || !view.payload?.biometric?.available || !view.payload.biometric.enrolled) return;
    encryptionPasswordModal.setError('');
    encryptionPasswordModal.setPayload({ ...view.payload, biometricBusy: true });
    encryptionPasswordModal.setBusy(true);
    try {
        await waitForBiometricCancellation();
        if (!isCurrent(request)) return;
        const result = await useBiometricUnlock();
        if (!isCurrent(request)) return;
        if (!result.ok) {
            encryptionPasswordModal.setError(humanizeBackendError(result.error));
            return;
        }
        await loadEncryptionStatus();
        if (!isCurrent(request)) return;
        window.dispatchEvent(new Event('tdrive:unlocked'));
        finish(true);
    } catch (err) {
        if (isCurrent(request)) encryptionPasswordModal.setError(humanizeBackendError(err));
    } finally {
        if (isCurrent(request)) {
            const payload = get(encryptionPasswordModal.state).payload;
            if (payload) encryptionPasswordModal.setPayload({ ...payload, biometricBusy: false });
            encryptionPasswordModal.setBusy(false);
        }
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
    // Concurrent encrypted reads observe one prompt and one eventual outcome.
    if (pending) return pending.promise;
    let resolve!: (ok: boolean) => void;
    const promise = new Promise<boolean>((done) => { resolve = done; });
    const request = { promise, resolve };
    pending = request;

    const showPrompt = async () => {
        if (!state.encryption?.loaded || !state.encryption?.hint) await loadEncryptionStatus();
        if (pending !== request) return;
        encryptionPasswordModal.open({ hint: String(state.encryption?.hint || '') });
        // Capability discovery must never prevent using the password.
        try {
            const biometric = await getBiometricStatus();
            if (!isCurrent(request)) return;
            const payload = get(encryptionPasswordModal.state).payload;
            if (payload) encryptionPasswordModal.setPayload({ ...payload, biometric });
        } catch {
            // No native capability is a normal password-only session.
        }
    };
    void showPrompt().catch(() => {
        if (pending === request) encryptionPasswordModal.open({ hint: '' });
    });
    return promise;
}
