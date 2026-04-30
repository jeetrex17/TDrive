// First-time encryption password setup. Triggered the very first time
// a user picks "Encrypt and upload" — never standalone, never as a
// drive-wide settings action. Resolves the returned promise to true on
// success, false on cancel.
//
// Folded together with unlock on the backend side: UnlockOrCreateVault
// creates a vault when none exists. We only call this modal when the
// status snapshot shows vault_exists=false; we don't show the strong
// "if you forget this password your files are gone" warning during a
// later normal unlock.

import { UnlockOrCreateVault } from '../../../wailsjs/go/main/App';
import { notify } from '../notifications.js';

let pending = null;

export function setupEncryptionSetupModal() {
    const modal = document.getElementById('encryption-setup-modal');
    if (!modal) return;
    const cancel = modal.querySelector('#encryption-setup-cancel');
    const confirm = modal.querySelector('#encryption-setup-confirm');
    const pwd = modal.querySelector('#encryption-setup-password');
    const pwd2 = modal.querySelector('#encryption-setup-password-confirm');
    const errEl = modal.querySelector('#encryption-setup-error');

    const finish = (ok) => {
        modal.style.display = 'none';
        if (pwd) pwd.value = '';
        if (pwd2) pwd2.value = '';
        if (errEl) { errEl.textContent = ''; errEl.style.display = 'none'; }
        if (pending) {
            const resolve = pending;
            pending = null;
            resolve(ok);
        }
    };
    const showError = (msg) => {
        if (!errEl) return;
        errEl.textContent = String(msg);
        errEl.style.display = 'block';
    };

    cancel.addEventListener('click', () => finish(false));
    modal.addEventListener('click', (e) => { if (e.target === modal) finish(false); });

    confirm.addEventListener('click', async () => {
        const a = String(pwd?.value || '');
        const b = String(pwd2?.value || '');
        if (a.length < 8) { showError('Use at least 8 characters.'); return; }
        if (a !== b) { showError('Passwords don’t match.'); return; }

        confirm.disabled = true;
        cancel.disabled = true;
        try {
            await UnlockOrCreateVault(a);
            finish(true);
            notify({
                level: 'success',
                title: 'Encryption is set up',
                body: 'These files will now be encrypted on this device.',
            });
        } catch (err) {
            showError(String(err));
        } finally {
            confirm.disabled = false;
            cancel.disabled = false;
        }
    });
}

export function openEncryptionSetupModal() {
    const modal = document.getElementById('encryption-setup-modal');
    if (!modal) return Promise.resolve(false);
    return new Promise((resolve) => {
        if (pending) {
            const prev = pending;
            pending = (ok) => { prev(ok); resolve(ok); };
            return;
        }
        pending = resolve;
        modal.style.display = 'flex';
        const pwd = modal.querySelector('#encryption-setup-password');
        setTimeout(() => pwd?.focus(), 0);
    });
}
