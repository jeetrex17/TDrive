// Logout confirmation modal. Two modes:
//   soft — drop the local Telegram session, keep the projection cache.
//   full — revoke server-side and wipe every TDrive file on disk.
//
// The modal trigger lives in the top-right profile menu, not here.

import { logout } from '../../api';
import { resetRenditions } from '../renditions/runtime';
import { showAuthView } from '../../ui/app/app-store';

import { notify, dismissNotification } from '../notifications';
import { humanizeBackendError } from '../errors';
import { logoutModal, type LogoutMode } from '../../ui/modals/logout-modal-store';


export function openLogoutModal() {
    logoutModal.open(null);
}

export async function confirmLogout(mode: LogoutMode): Promise<void> {
    const progressId = notify({
        id: 'logout-progress',
        level: 'info',
        title: 'Logging out…',
        sticky: true,
        spinner: true,
    });
    logoutModal.setBusy(true);
    resetRenditions();
    try {
        await logout(mode);
        // Backend issues runtime.Quit on success, so this fallback only
        // runs if the process somehow stays alive (e.g. dev hot-reload).
        logoutModal.close();
        dismissNotification(progressId);
        showAuthView('phone');
    } catch (err) {
        dismissNotification(progressId);
        notify({
            level: 'error',
            title: 'Could not log out',
            body: humanizeBackendError(err),
        });
    } finally {
        logoutModal.setBusy(false);
    }
}
