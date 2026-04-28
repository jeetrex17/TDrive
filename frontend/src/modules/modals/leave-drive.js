// "Leave drive" confirm modal.

import { leaveSharedDrive } from '../channels.js';

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
        const status = document.getElementById('status-msg');
        if (status) status.innerText = 'Leaving drive...';
        confirm.disabled = true;
        try {
            await leaveSharedDrive(pendingTarget.id);
            close();
        } catch (err) {
            alert('Failed to leave drive: ' + err);
        } finally {
            confirm.disabled = false;
            if (status) status.innerText = 'Ready';
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
