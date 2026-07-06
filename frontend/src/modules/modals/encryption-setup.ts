// First-time encryption password setup. Triggered when a user first
// chooses "Encrypt before upload". This password protects every
// encrypted personal file; if forgotten, those files cannot be recovered.

import { CreateEncryptionPassword } from '../../../wailsjs/go/main/App';
import { notify } from '../notifications';
import { loadEncryptionStatus } from '../encryption';
import EncryptionSetupModal from '../../ui/modals/EncryptionSetupModal.svelte';
import { encryptionSetupModal } from '../../ui/modals/encryption-setup-modal-store';
import { mountSvelte, type SvelteMountHandle } from '../../ui/mount';

let encryptionSetupModalHandle: SvelteMountHandle<Record<string, unknown>> | null = null;
let pending: ((ok: boolean) => void) | null = null;

function finish(ok: boolean): void {
    encryptionSetupModal.close();
    if (pending) {
        const resolve = pending;
        pending = null;
        resolve(ok);
    }
}

export function setupEncryptionSetupModal() {
    const modal = document.getElementById('encryption-setup-modal');
    if (!modal || encryptionSetupModalHandle) return;

    modal.replaceChildren();
    encryptionSetupModalHandle = mountSvelte(EncryptionSetupModal, {
        target: modal,
        props: {
            onCancel: () => finish(false),
            onSubmit: submitSetup,
        },
    });
}

async function submitSetup(password: string, confirmPassword: string, hint: string): Promise<void> {
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
        await CreateEncryptionPassword(password, hint);
        await loadEncryptionStatus();
        finish(true);
        notify({
            level: 'success',
            title: 'Encryption password created',
            body: 'Encrypted uploads will be protected before they leave this device.',
        });
    } catch (err) {
        encryptionSetupModal.setError(String(err));
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
