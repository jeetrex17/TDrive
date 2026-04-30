// Logout confirmation modal. Two modes:
//   soft — drop the local Telegram session, keep the projection cache.
//   full — revoke server-side and wipe every TDrive file on disk.

import { Logout } from '../../../wailsjs/go/main/App';
import { showAuthWrapper, hideAllScreens } from '../auth.js';
import { notify, dismissNotification } from '../notifications.js';

export function setupLogoutModal() {
    const modal = document.getElementById('logout-modal');
    const trigger = document.getElementById('logout-btn');
    const cancel = document.getElementById('logout-cancel');
    const confirm = document.getElementById('logout-confirm');
    if (!modal || !trigger || !cancel || !confirm) return;

    const close = () => {
        modal.style.display = 'none';
    };

    const open = () => {
        const soft = modal.querySelector('input[name="logout-mode"][value="soft"]');
        if (soft) soft.checked = true;
        modal.style.display = 'flex';
    };

    trigger.addEventListener('click', open);
    cancel.addEventListener('click', close);
    modal.addEventListener('click', (e) => { if (e.target === modal) close(); });

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
            close();
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
