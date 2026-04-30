// "Join shared drive" modal.

import { joinSharedDrive } from '../channels.js';
import { notify, dismissNotification } from '../notifications.js';

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
        const progressId = notify({
            id: 'joining-drive',
            level: 'info',
            title: 'Joining drive…',
            body: 'If the drive has lots of history, this can take a few seconds.',
            sticky: true,
            spinner: true,
        });
        go.disabled = true;
        try {
            const result = await joinSharedDrive(link);
            close();
            dismissNotification(progressId);
            if (result?.status === 'pending') {
                notify({
                    level: 'success',
                    title: 'Join request sent',
                    body: 'The drive will appear after an admin approves you.',
                });
            } else {
                notify({
                    level: 'success',
                    title: 'Joined drive',
                    body: result?.channel?.title ? String(result.channel.title) : '',
                });
            }
        } catch (err) {
            dismissNotification(progressId);
            notify({
                level: 'error',
                title: 'Could not join drive',
                body: String(err),
            });
        } finally {
            go.disabled = false;
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
