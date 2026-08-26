import { createModalController } from './modal-store';

export interface ImportOptionsPlan {
    files: number;
    folders: number;
    bytes: number;
    oversize: number;
    archives: number;
    ignored: number;
    maxItems: number;
    limitExceeded: boolean;
    maxBytes: number;
    errorCount?: number;
    errors?: string[];
}

export interface ImportOptionsPayload {
    plan: ImportOptionsPlan;
    personal: boolean;
    hasArchives: boolean;
    replanning?: boolean;
}

export const importOptionsModal = createModalController<ImportOptionsPayload>();
