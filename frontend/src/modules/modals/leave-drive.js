// "Leave drive" confirm modal.

import { leaveSharedDrive } from '../channels.js';
import { notify, dismissNotification } from '../notifications.js';
import { installModalA11y } from './modal-a11y.js';

let pendingTarget = null;
let a11y = null;

export function setupLeaveDriveModal() {
    const modal = document.getElementById('leave-drive-modal');
    const cancel = document.getElementById('leave-drive-cancel');
    const confirm = document.getElementById('leave-drive-confirm');
    const subtitle = document.getElementById('leave-drive-subtitle');
    if (!modal || !cancel || !confirm) return;

    const close = () => {
        a11y?.deactivate();
        modal.style.display = 'none';
        pendingTarget = null;
    };

    cancel.addEventListener('click', close);
    modal.addEventListener('click', (e) => { if (e.target === modal) close(); });
    a11y = installModalA11y(modal, { requestClose: close, initialFocus: cancel });

    confirm.addEventListener('click', async () => {
        if (!pendingTarget) { close(); return; }
        const progressId = notify({
            id: 'leaving-drive',
            level: 'info',
            title: 'Leaving drive…',
            sticky: true,
            spinner: true,
        });
        confirm.disabled = true;
        try {
            const target = pendingTarget;
            await leaveSharedDrive(target.id);
            close();
            dismissNotification(progressId);
            notify({
                level: 'success',
                title: 'Left drive',
                body: target.title ? String(target.title) : '',
            });
        } catch (err) {
            dismissNotification(progressId);
            notify({
                level: 'error',
                title: 'Could not leave drive',
                body: String(err),
            });
        } finally {
            confirm.disabled = false;
        }
    });

    if (subtitle) {
        subtitle.dataset.template = subtitle.textContent || '';
    }
}

export function openLeaveDriveModal({ id, title }) {
    const modal = document.getElementById('leave-drive-modal');
    const subtitle = document.getElementById('leave-drive-subtitle');
    if (!modal) return;
    pendingTarget = { id: Number(id), title: String(title || '') };
    if (subtitle) {
        subtitle.textContent = `Leave "${pendingTarget.title}"? You can rejoin later with the invite link.`;
    }
    modal.style.display = 'flex';
    a11y?.activate();
}
