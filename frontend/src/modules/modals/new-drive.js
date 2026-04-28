// "New shared drive" modal.

import { createSharedDrive } from '../channels.js';
import { openShareDriveModal } from './share-drive.js';

export function setupNewDriveModal() {
    const modal = document.getElementById('new-drive-modal');
    const cancel = document.getElementById('new-drive-cancel');
    const create = document.getElementById('new-drive-create');
    const input = document.getElementById('new-drive-name');
    if (!modal || !cancel || !create || !input) return;

    const close = () => {
        modal.style.display = 'none';
        input.value = '';
    };

    cancel.addEventListener('click', close);
    modal.addEventListener('click', (e) => { if (e.target === modal) close(); });

    const submit = async () => {
        const title = String(input.value || '').trim();
        if (!title) return;
        const status = document.getElementById('status-msg');
        if (status) status.innerText = 'Creating drive...';
        create.disabled = true;
        try {
            const info = await createSharedDrive(title);
            close();
            if (info?.invite_link) {
                openShareDriveModal(String(info.invite_link));
            }
        } catch (err) {
            alert('Failed to create drive: ' + err);
        } finally {
            create.disabled = false;
            if (status) status.innerText = 'Ready';
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
