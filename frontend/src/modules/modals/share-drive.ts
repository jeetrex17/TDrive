// "Invite link" share modal — shows the t.me link with a Copy button.

import { shareDriveModal } from '../../ui/modals/share-drive-modal-store';


export function openShareDriveModal(link: string, options: { approvalRequired?: boolean } = {}) {
    shareDriveModal.open({
        link: String(link || ''),
        approvalRequired: Boolean(options.approvalRequired),
    });
}
