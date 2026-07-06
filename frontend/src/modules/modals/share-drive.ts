// "Invite link" share modal — shows the t.me link with a Copy button.

import ShareDriveModal from '../../ui/modals/ShareDriveModal.svelte';
import { shareDriveModal } from '../../ui/modals/share-drive-modal-store';
import { mountSvelte, type SvelteMountHandle } from '../../ui/mount';

let shareDriveModalHandle: SvelteMountHandle<Record<string, unknown>> | null = null;

export function setupShareDriveModal() {
    const modal = document.getElementById('share-drive-modal');
    if (!modal || shareDriveModalHandle) return;

    modal.replaceChildren();
    shareDriveModalHandle = mountSvelte(ShareDriveModal, {
        target: modal,
        props: {},
    });
}

export function openShareDriveModal(link: string, options: { approvalRequired?: boolean } = {}) {
    shareDriveModal.open({
        link: String(link || ''),
        approvalRequired: Boolean(options.approvalRequired),
    });
}
