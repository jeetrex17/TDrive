import { createModalController } from './modal-store';

export interface ImportOptionsPlan {
    files: number;
    folders: number;
    bytes: number;
    oversize: number;
    archives: number;
    maxBytes: number;
    errors?: string[];
}

export interface ImportOptionsPayload {
    plan: ImportOptionsPlan;
    personal: boolean;
    hasArchives: boolean;
}

export const importOptionsModal = createModalController<ImportOptionsPayload>();
