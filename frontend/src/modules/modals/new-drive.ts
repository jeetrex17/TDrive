// "New shared drive" modal.

import { createSharedDrive } from '../channels';
import { openShareDriveModal } from './share-drive';
import { notify, dismissNotification } from '../notifications';
import { humanizeBackendError } from '../errors';
import { newDriveModal } from '../../ui/modals/new-drive-modal-store';


export function openNewDriveModal() {
    newDriveModal.open(null);
}

export async function submitNewDrive(title: string, requireApproval: boolean): Promise<void> {
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
        if (info.inviteLink) {
            openShareDriveModal(info.inviteLink, { approvalRequired: requireApproval });
        }
    } catch (err) {
        dismissNotification(progressId);
        notify({
            level: 'error',
            title: 'Could not create drive',
            body: humanizeBackendError(err),
        });
    } finally {
        newDriveModal.setBusy(false);
    }
}
