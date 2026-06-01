// Password prompt for encrypted uploads, downloads, and previews.
// On success the backend remembers the decrypted master key in memory
// until the app exits, so users do not re-enter the password per file.

import { UseEncryptionPassword } from '../../../wailsjs/go/main/App';
import { loadEncryptionStatus } from '../encryption.js';
import { state } from '../../state.js';

let pending = null;

export function setupEncryptionPasswordModal() {
    const modal = document.getElementById('encryption-password-modal');
    if (!modal) return;
    const cancel = modal.querySelector('#encryption-password-cancel');
    const confirm = modal.querySelector('#encryption-password-confirm');
    const pwd = modal.querySelector('#encryption-password-input');
    const hintRow = modal.querySelector('#encryption-password-hint');
    const hintText = modal.querySelector('#encryption-password-hint-text');
    const errEl = modal.querySelector('#encryption-password-error');

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
            showError('Enter your encryption password.');
            return;
        }
        confirm.disabled = true;
        cancel.disabled = true;
        try {
            await UseEncryptionPassword(value);
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

export function openEncryptionPasswordModal() {
    const modal = document.getElementById('encryption-password-modal');
    if (!modal) return Promise.resolve(false);
    const hintRow = modal.querySelector('#encryption-password-hint');
    const hintText = modal.querySelector('#encryption-password-hint-text');
    return new Promise((resolve) => {
        if (pending) {
            const prev = pending;
            pending = (ok) => { prev(ok); resolve(ok); };
            return;
        }
        pending = resolve;
        const showPrompt = async () => {
            if (!state.encryption?.loaded || !state.encryption?.hint) {
                await loadEncryptionStatus();
            }
            modal.style.display = 'flex';
            const hint = String(state.encryption?.hint || '').trim();
            if (hintRow && hintText) {
                hintText.textContent = hint;
                hintRow.style.display = hint ? 'block' : 'none';
            }
            const pwd = modal.querySelector('#encryption-password-input');
            setTimeout(() => pwd?.focus(), 0);
        };
        showPrompt().catch(() => {
            modal.style.display = 'flex';
            if (hintRow && hintText) {
                hintText.textContent = '';
                hintRow.style.display = 'none';
            }
            const pwd = modal.querySelector('#encryption-password-input');
            setTimeout(() => pwd?.focus(), 0);
        });
    });
}
