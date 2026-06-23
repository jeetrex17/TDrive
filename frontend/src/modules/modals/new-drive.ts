// "New shared drive" modal.

import { createSharedDrive } from '../channels';
import { openShareDriveModal } from './share-drive';
import { notify, dismissNotification } from '../notifications';
import { installModalA11y } from './modal-a11y';

let a11y: ReturnType<typeof installModalA11y> | null = null;

export function setupNewDriveModal() {
    const modal = document.getElementById('new-drive-modal');
    const cancel = document.getElementById('new-drive-cancel');
    const create = document.getElementById('new-drive-create') as HTMLButtonElement | null;
    const input = document.getElementById('new-drive-name') as HTMLInputElement | null;
    const approval = document.getElementById('new-drive-require-approval') as HTMLInputElement | null;
    if (!modal || !cancel || !create || !input) return;

    const close = () => {
        a11y?.deactivate();
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
    });

    a11y = installModalA11y(modal, {
        requestClose: close,
        initialFocus: input,
        restoreFocus: '#drives-nav',
    });
}

export function openNewDriveModal() {
    const modal = document.getElementById('new-drive-modal');
    const input = document.getElementById('new-drive-name');
    if (!modal || !input) return;
    modal.style.display = 'flex';
    a11y?.activate();
}
