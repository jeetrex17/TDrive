// "Join shared drive" modal.

import { joinSharedDrive } from '../channels';
import { notify, dismissNotification } from '../notifications';
import JoinDriveModal from '../../ui/modals/JoinDriveModal.svelte';
import { joinDriveModal } from '../../ui/modals/join-drive-modal-store';
import { mountSvelte, type SvelteMountHandle } from '../../ui/mount';

let joinDriveModalHandle: SvelteMountHandle<Record<string, unknown>> | null = null;

export function setupJoinDriveModal() {
    const modal = document.getElementById('join-drive-modal');
    if (!modal || joinDriveModalHandle) return;

    modal.replaceChildren();
    joinDriveModalHandle = mountSvelte(JoinDriveModal, {
        target: modal,
        props: {
            onSubmit: submitJoinDrive,
        },
    });
}

export function openJoinDriveModal() {
    joinDriveModal.open(null);
}

async function submitJoinDrive(link: string): Promise<void> {
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
            body: String(err),
        });
    } finally {
        joinDriveModal.setBusy(false);
    }
}
