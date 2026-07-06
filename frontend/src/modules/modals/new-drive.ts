// "New shared drive" modal.

import { createSharedDrive } from '../channels';
import { openShareDriveModal } from './share-drive';
import { notify, dismissNotification } from '../notifications';
import NewDriveModal from '../../ui/modals/NewDriveModal.svelte';
import { newDriveModal } from '../../ui/modals/new-drive-modal-store';
import { mountSvelte, type SvelteMountHandle } from '../../ui/mount';

let newDriveModalHandle: SvelteMountHandle<Record<string, unknown>> | null = null;

export function setupNewDriveModal() {
    const modal = document.getElementById('new-drive-modal');
    if (!modal || newDriveModalHandle) return;

    modal.replaceChildren();
    newDriveModalHandle = mountSvelte(NewDriveModal, {
        target: modal,
        props: {
            onSubmit: submitNewDrive,
        },
    });
}

export function openNewDriveModal() {
    newDriveModal.open(null);
}

async function submitNewDrive(title: string, requireApproval: boolean): Promise<void> {
    const progressId = notify({
        id: 'creating-drive',
        level: 'info',
        title: 'Creating drive…',
        sticky: true,
        spinner: true,
    });
    newDriveModal.setBusy(true);
    try {
        const info = await createSharedDrive(title, requireApproval);
        newDriveModal.close();
        dismissNotification(progressId);
        notify({ level: 'success', title: `Drive "${title}" created` });
        if (info?.invite_link) {
            openShareDriveModal(String(info.invite_link), { approvalRequired: requireApproval });
        }
    } catch (err) {
        dismissNotification(progressId);
        notify({
            level: 'error',
            title: 'Could not create drive',
            body: String(err),
        });
    } finally {
        newDriveModal.setBusy(false);
    }
}
