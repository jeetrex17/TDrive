import { createModalController } from './modal-store';
import type { ImportFileConflict } from '../../types';

export interface ImportChoice {
    encrypt: boolean;
    extract: boolean;
    folderMerge?: ImportFileConflict;
}

export interface ImportOptionsPlan {
    files: number;
    folders: number;
    bytes: number;
    oversize: number;
    empty?: number;
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
    allowFolderMerge?: boolean;
    replanning?: boolean;
    queued?: boolean;
    destination?: string;
    names?: readonly string[];
    /** The encryption choice the dialog opens with. */
    encrypt?: boolean;
}

export const importOptionsModal = createModalController<ImportOptionsPayload>();
