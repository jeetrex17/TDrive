// "Join shared drive" modal.

import { joinSharedDrive } from '../channels.js';

export function setupJoinDriveModal() {
    const modal = document.getElementById('join-drive-modal');
    const cancel = document.getElementById('join-drive-cancel');
    const go = document.getElementById('join-drive-go');
    const input = document.getElementById('join-drive-link');
    if (!modal || !cancel || !go || !input) return;

    const close = () => {
        modal.style.display = 'none';
        input.value = '';
    };

    cancel.addEventListener('click', close);
    modal.addEventListener('click', (e) => { if (e.target === modal) close(); });

    const submit = async () => {
        const link = String(input.value || '').trim();
        if (!link) return;
        const status = document.getElementById('status-msg');
        if (status) status.innerText = 'Joining drive...';
        go.disabled = true;
        try {
            const result = await joinSharedDrive(link);
            close();
            if (result?.status === 'pending') {
                alert('Join request sent. The drive will appear after an admin approves you; use the pending row in the sidebar to check later.');
            }
        } catch (err) {
            alert('Failed to join drive: ' + err);
        } finally {
            go.disabled = false;
            if (status) status.innerText = 'Ready';
        }
    };

    go.addEventListener('click', submit);
    input.addEventListener('keydown', (e) => {
        if (e.key === 'Enter') submit();
        if (e.key === 'Escape') close();
    });
}

export function openJoinDriveModal() {
    const modal = document.getElementById('join-drive-modal');
    const input = document.getElementById('join-drive-link');
    if (!modal || !input) return;
    modal.style.display = 'flex';
    setTimeout(() => input.focus(), 0);
}
