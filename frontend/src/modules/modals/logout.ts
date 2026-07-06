// Logout confirmation modal. Two modes:
//   soft — drop the local Telegram session, keep the projection cache.
//   full — revoke server-side and wipe every TDrive file on disk.
//
// The modal trigger lives in the top-right profile menu, not here.

import { Logout } from '../../../wailsjs/go/main/App';
import { showAuthWrapper, hideAllScreens } from '../auth';
import { notify, dismissNotification } from '../notifications';
import LogoutModal from '../../ui/modals/LogoutModal.svelte';
import { logoutModal, type LogoutMode } from '../../ui/modals/logout-modal-store';
import { mountSvelte, type SvelteMountHandle } from '../../ui/mount';

let logoutModalHandle: SvelteMountHandle<Record<string, unknown>> | null = null;

export function setupLogoutModal() {
    const modal = document.getElementById('logout-modal');
    if (!modal || logoutModalHandle) return;

    modal.replaceChildren();
    logoutModalHandle = mountSvelte(LogoutModal, {
        target: modal,
        props: {
            onConfirm: confirmLogout,
        },
    });
}

export function openLogoutModal() {
    logoutModal.open(null);
}

async function confirmLogout(mode: LogoutMode): Promise<void> {
    const progressId = notify({
        id: 'logout-progress',
        level: 'info',
        title: 'Logging out…',
        sticky: true,
        spinner: true,
    });
    logoutModal.setBusy(true);
    try {
        await Logout(mode);
        // Backend issues runtime.Quit on success, so this fallback only
        // runs if the process somehow stays alive (e.g. dev hot-reload).
        logoutModal.close();
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
        logoutModal.setBusy(false);
    }
}
