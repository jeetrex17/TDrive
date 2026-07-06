// Password prompt for encrypted uploads, downloads, and previews.
// On success the backend remembers the decrypted master key in memory
// until the app exits, so users do not re-enter the password per file.

import { UseEncryptionPassword } from '../../../wailsjs/go/main/App';
import { loadEncryptionStatus } from '../encryption';
import { state } from '../../state';
import EncryptionPasswordModal from '../../ui/modals/EncryptionPasswordModal.svelte';
import { encryptionPasswordModal } from '../../ui/modals/encryption-password-modal-store';
import { mountSvelte, type SvelteMountHandle } from '../../ui/mount';

let encryptionPasswordModalHandle: SvelteMountHandle<Record<string, unknown>> | null = null;
let pending: ((ok: boolean) => void) | null = null;

function finish(ok: boolean): void {
    encryptionPasswordModal.close();
    if (pending) {
        const resolve = pending;
        pending = null;
        resolve(ok);
    }
}

export function setupEncryptionPasswordModal() {
    const modal = document.getElementById('encryption-password-modal');
    if (!modal || encryptionPasswordModalHandle) return;

    modal.replaceChildren();
    encryptionPasswordModalHandle = mountSvelte(EncryptionPasswordModal, {
        target: modal,
        props: {
            onCancel: () => finish(false),
            onSubmit: submitPassword,
        },
    });
}

async function submitPassword(password: string): Promise<void> {
    if (!password) {
        encryptionPasswordModal.setError('Enter your encryption password.');
        return;
    }
    encryptionPasswordModal.setError('');
    encryptionPasswordModal.setBusy(true);
    try {
        await UseEncryptionPassword(password);
        await loadEncryptionStatus();
        finish(true);
    } catch (err) {
        encryptionPasswordModal.setError(String(err));
    } finally {
        encryptionPasswordModal.setBusy(false);
    }
}

// callWithPasswordRetry runs a backend binding that returns "Error: ..." strings.
// If it fails with "encryption password required" (a locked vault), it prompts
// for the password once and retries. Used by rename/move/delete on encrypted
// files and folders.
export async function callWithPasswordRetry(call: () => Promise<any>): Promise<any> {
    let res = await call();
    if (typeof res === "string" && res.startsWith("Error") && /encryption password required/i.test(res)) {
        const ok = await openEncryptionPasswordModal();
        if (!ok) return "Error: Encryption password required";
        res = await call();
    }
    return res;
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
