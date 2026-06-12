// Password prompt for encrypted uploads, downloads, and previews.
// On success the backend remembers the decrypted master key in memory
// until the app exits, so users do not re-enter the password per file.

import { UseEncryptionPassword } from '../../../wailsjs/go/main/App';
import { loadEncryptionStatus } from '../encryption';
import { state } from '../../state';

let pending: any = null;

export function setupEncryptionPasswordModal() {
    const modal = document.getElementById('encryption-password-modal');
    if (!modal) return;
    const cancel = modal.querySelector('#encryption-password-cancel') as HTMLButtonElement | null;
    const confirm = modal.querySelector('#encryption-password-confirm') as HTMLButtonElement | null;
    const pwd = modal.querySelector('#encryption-password-input') as HTMLInputElement | null;
    const hintRow = modal.querySelector('#encryption-password-hint') as HTMLElement | null;
    const hintText = modal.querySelector('#encryption-password-hint-text') as HTMLElement | null;
    const errEl = modal.querySelector('#encryption-password-error') as HTMLElement | null;

    const finish = (ok: any) => {
        modal.style.display = 'none';
        if (pwd) pwd.value = '';
        if (errEl) { errEl.textContent = ''; errEl.style.display = 'none'; }
        if (pending) {
            const resolve = pending;
            pending = null;
            resolve(ok);
        }
    };
    const showError = (msg: any) => {
        if (!errEl) return;
        errEl.textContent = String(msg);
        errEl.style.display = 'block';
    };

    cancel!.addEventListener('click', () => finish(false));
    modal.addEventListener('click', (e) => { if (e.target === modal) finish(false); });

    confirm!.addEventListener('click', async () => {
        const value = String(pwd?.value || '');
        if (!value) {
            showError('Enter your encryption password.');
            return;
        }
        confirm!.disabled = true;
        cancel!.disabled = true;
        try {
            await UseEncryptionPassword(value);
            await loadEncryptionStatus();
            finish(true);
        } catch (err) {
            showError(String(err));
        } finally {
            confirm!.disabled = false;
            cancel!.disabled = false;
        }
    });

    if (pwd) {
        pwd.addEventListener('keydown', (e: KeyboardEvent) => {
            if (e.key === 'Enter') confirm!.click();
        });
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

export function openEncryptionPasswordModal() {
    const modal = document.getElementById('encryption-password-modal');
    if (!modal) return Promise.resolve(false);
    const hintRow = modal.querySelector('#encryption-password-hint') as HTMLElement | null;
    const hintText = modal.querySelector('#encryption-password-hint-text') as HTMLElement | null;
    return new Promise((resolve) => {
        if (pending) {
            const prev = pending;
            pending = (ok: any) => { prev(ok); resolve(ok); };
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
            const pwd = modal.querySelector('#encryption-password-input') as HTMLInputElement | null;
            setTimeout(() => pwd?.focus(), 0);
        };
        showPrompt().catch(() => {
            modal.style.display = 'flex';
            if (hintRow && hintText) {
                hintText.textContent = '';
                hintRow.style.display = 'none';
            }
            const pwd = modal.querySelector('#encryption-password-input') as HTMLInputElement | null;
            setTimeout(() => pwd?.focus(), 0);
        });
    });
}
