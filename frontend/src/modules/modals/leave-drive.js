// "Leave drive" confirm modal.

import { leaveSharedDrive } from '../channels.js';
import { notify, dismissNotification } from '../notifications.js';

let pendingTarget = null;

export function setupLeaveDriveModal() {
    const modal = document.getElementById('leave-drive-modal');
    const cancel = document.getElementById('leave-drive-cancel');
    const confirm = document.getElementById('leave-drive-confirm');
    const subtitle = document.getElementById('leave-drive-subtitle');
    if (!modal || !cancel || !confirm) return;

    const close = () => {
        modal.style.display = 'none';
        pendingTarget = null;
    };

    cancel.addEventListener('click', close);
    modal.addEventListener('click', (e) => { if (e.target === modal) close(); });

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
}
