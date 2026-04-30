// "New shared drive" modal.

import { createSharedDrive } from '../channels.js';
import { openShareDriveModal } from './share-drive.js';
import { notify, dismissNotification } from '../notifications.js';

export function setupNewDriveModal() {
    const modal = document.getElementById('new-drive-modal');
    const cancel = document.getElementById('new-drive-cancel');
    const create = document.getElementById('new-drive-create');
    const input = document.getElementById('new-drive-name');
    const approval = document.getElementById('new-drive-require-approval');
    if (!modal || !cancel || !create || !input) return;

    const close = () => {
        modal.style.display = 'none';
        input.value = '';
        if (approval) approval.checked = false;
    };

    cancel.addEventListener('click', close);
    modal.addEventListener('click', (e) => { if (e.target === modal) close(); });

    const submit = async () => {
        const title = String(input.value || '').trim();
        if (!title) return;
        const progressId = notify({
            id: 'creating-drive',
            level: 'info',
            title: 'Creating drive…',
            sticky: true,
            spinner: true,
        });
        create.disabled = true;
        try {
            const info = await createSharedDrive(title, Boolean(approval?.checked));
            close();
            dismissNotification(progressId);
            notify({ level: 'success', title: `Drive "${title}" created` });
            if (info?.invite_link) {
                openShareDriveModal(String(info.invite_link), { approvalRequired: Boolean(approval?.checked) });
            }
        } catch (err) {
            dismissNotification(progressId);
            notify({
                level: 'error',
                title: 'Could not create drive',
                body: String(err),
            });
        } finally {
            create.disabled = false;
        }
    };

    create.addEventListener('click', submit);
    input.addEventListener('keydown', (e) => {
        if (e.key === 'Enter') submit();
        if (e.key === 'Escape') close();
    });
}

export function openNewDriveModal() {
    const modal = document.getElementById('new-drive-modal');
    const input = document.getElementById('new-drive-name');
    if (!modal || !input) return;
    modal.style.display = 'flex';
    setTimeout(() => input.focus(), 0);
}
