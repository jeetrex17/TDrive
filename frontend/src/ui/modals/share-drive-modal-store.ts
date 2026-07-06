import { createModalController } from './modal-store';

export interface ShareDrivePayload {
    link: string;
    approvalRequired: boolean;
}

export const shareDriveModal = createModalController<ShareDrivePayload>();
