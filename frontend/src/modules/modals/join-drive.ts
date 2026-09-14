// "Join shared drive" modal.

import { joinSharedDrive } from '../channels';
import { notify, dismissNotification } from '../notifications';
import { humanizeBackendError } from '../errors';
import { joinDriveModal } from '../../ui/modals/join-drive-modal-store';


export function openJoinDriveModal() {
    joinDriveModal.open(null);
}

export async function submitJoinDrive(link: string): Promise<void> {
    const progressId = notify({
        id: 'joining-drive',
        level: 'info',
        title: 'Joining drive…',
        body: 'If the drive has lots of history, this can take a few seconds.',
        sticky: true,
        spinner: true,
    });
    joinDriveModal.setBusy(true);
    try {
        const result = await joinSharedDrive(link);
        joinDriveModal.close();
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
            body: humanizeBackendError(err),
        });
    } finally {
        joinDriveModal.setBusy(false);
    }
}
