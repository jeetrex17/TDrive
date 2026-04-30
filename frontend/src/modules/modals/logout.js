// Logout confirmation modal. Two modes:
//   soft — drop the local Telegram session, keep the projection cache.
//   full — revoke server-side and wipe every TDrive file on disk.
//
// The modal trigger lives in the top-right profile menu, not here.

import { Logout } from '../../../wailsjs/go/main/App';
import { showAuthWrapper, hideAllScreens } from '../auth.js';
import { notify, dismissNotification } from '../notifications.js';

export function setupLogoutModal() {
    const modal = document.getElementById('logout-modal');
    const cancel = document.getElementById('logout-cancel');
    const confirm = document.getElementById('logout-confirm');
    if (!modal || !cancel || !confirm) return;

    cancel.addEventListener('click', () => closeLogoutModal());
    modal.addEventListener('click', (e) => { if (e.target === modal) closeLogoutModal(); });

    confirm.addEventListener('click', async () => {
        const selected = modal.querySelector('input[name="logout-mode"]:checked');
        const mode = selected ? selected.value : 'soft';

        const progressId = notify({
            id: 'logout-progress',
            level: 'info',
            title: 'Logging out…',
            sticky: true,
            spinner: true,
        });
        confirm.disabled = true;
        cancel.disabled = true;
        try {
            await Logout(mode);
            // Backend issues runtime.Quit on success, so this fallback only
            // runs if the process somehow stays alive (e.g. dev hot-reload).
            closeLogoutModal();
            dismissNotification(progressId);
            hideAllScreens();
            showAuthWrapper();
        } catch (err) {
            dismissNotification(progressId);
            notify({
                level: 'error',
                title: 'Could not log out',
                body: String(err),
            });
        } finally {
            confirm.disabled = false;
            cancel.disabled = false;
        }
    });
}

export function openLogoutModal() {
    const modal = document.getElementById('logout-modal');
    if (!modal) return;
    const soft = modal.querySelector('input[name="logout-mode"][value="soft"]');
    if (soft) soft.checked = true;
    modal.style.display = 'flex';
}

function closeLogoutModal() {
    const modal = document.getElementById('logout-modal');
    if (modal) modal.style.display = 'none';
}
