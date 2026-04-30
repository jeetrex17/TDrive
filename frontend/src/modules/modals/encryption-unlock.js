// Unlock modal — used both proactively (user clicks the locked pill)
// and reactively (an upload/download/preview tried while locked).
//
// openEncryptionUnlockModal returns a Promise that resolves to true on
// successful unlock and false on cancel. Callers chain off this so the
// triggering action can resume after the modal closes.

import { UnlockOrCreateVault } from '../../../wailsjs/go/main/App';
import { loadEncryptionStatus } from '../encryption.js';
import { notify } from '../notifications.js';

let pending = null;

export function setupEncryptionUnlockModal() {
    const modal = document.getElementById('encryption-unlock-modal');
    if (!modal) return;
    const cancel = modal.querySelector('#encryption-unlock-cancel');
    const confirm = modal.querySelector('#encryption-unlock-confirm');
    const pwd = modal.querySelector('#encryption-unlock-password');
    const errEl = modal.querySelector('#encryption-unlock-error');

    const finish = (ok) => {
        modal.style.display = 'none';
        if (pwd) pwd.value = '';
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
        const value = String(pwd?.value || '');
        if (!value) {
            showError('Enter your password.');
            return;
        }
        confirm.disabled = true;
        cancel.disabled = true;
        try {
            await UnlockOrCreateVault(value);
            await loadEncryptionStatus();
            finish(true);
        } catch (err) {
            showError(String(err));
        } finally {
            confirm.disabled = false;
            cancel.disabled = false;
        }
    });

    if (pwd) {
        pwd.addEventListener('keydown', (e) => {
            if (e.key === 'Enter') confirm.click();
        });
    }
}

export function openEncryptionUnlockModal() {
    const modal = document.getElementById('encryption-unlock-modal');
    if (!modal) return Promise.resolve(false);
    return new Promise((resolve) => {
        if (pending) {
            // Don't stack prompts; bind the new caller to the open one.
            const prev = pending;
            pending = (ok) => { prev(ok); resolve(ok); };
            return;
        }
        pending = resolve;
        modal.style.display = 'flex';
        const pwd = modal.querySelector('#encryption-unlock-password');
        setTimeout(() => pwd?.focus(), 0);
    });
}
