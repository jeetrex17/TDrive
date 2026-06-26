// "Leave drive" confirm modal.

import { leaveSharedDrive } from '../channels';
import { notify, dismissNotification } from '../notifications';
import LeaveDriveModal from '../../ui/modals/LeaveDriveModal.svelte';
import {
    closeLeaveDriveModalView,
    openLeaveDriveModalView,
    setLeaveDriveModalInFlight,
    type LeaveDriveTarget,
} from '../../ui/modals/leave-drive-modal-store';
import { mountSvelte, type SvelteMountHandle } from '../../ui/mount';

let leaveDriveModalHandle: SvelteMountHandle<Record<string, unknown>> | null = null;
let inFlight = false;

export function setupLeaveDriveModal() {
    const modal = document.getElementById('leave-drive-modal');
    if (!modal || leaveDriveModalHandle) return;

    modal.replaceChildren();
    leaveDriveModalHandle = mountSvelte(LeaveDriveModal, {
        target: modal,
        props: {
            onConfirm: confirmLeaveDrive,
        },
    });
}

async function confirmLeaveDrive(target: LeaveDriveTarget): Promise<void> {
    if (inFlight) return;
    inFlight = true;
    setLeaveDriveModalInFlight(true);

    const progressId = notify({
        id: 'leaving-drive',
        level: 'info',
        title: 'Leaving drive…',
        sticky: true,
        spinner: true,
    });

    try {
        await leaveSharedDrive(target.id);
        closeLeaveDriveModalView();
        dismissNotification(progressId);
        notify({
            level: 'success',
            title: 'Left drive',
            body: target.title ? String(target.title) : '',
        });
    } catch (err) {
        dismissNotification(progressId);
        notify({
            level: 'error',
            title: 'Could not leave drive',
            body: String(err),
        });
    } finally {
        inFlight = false;
        setLeaveDriveModalInFlight(false);
    }
}

export function openLeaveDriveModal({ id, title }: { id: number | string; title?: string }) {
    openLeaveDriveModalView({ id: Number(id), title: String(title || '') });
}
