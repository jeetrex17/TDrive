import { createModalController } from './modal-store';
import type { UploadFailure } from '../../types';
import { writable } from 'svelte/store';

export interface FailedUploadsPayload {
    paths: string[];
    failures: UploadFailure[];
    parentId: string;
    encrypt: boolean;
    driveId: number | null;
    driveTitle: string;
}

export const failedUploadsModal = createModalController<FailedUploadsPayload>();

// Session-only sources and actions must never enter persisted notification history.
export const failedUploadReports = writable<FailedUploadsPayload[]>([]);

export function rememberFailedUploadReport(payload: FailedUploadsPayload): void {
    failedUploadReports.update((reports) => [payload, ...reports].slice(0, 10));
}

export function dismissFailedUploadReport(payload: FailedUploadsPayload): void {
    failedUploadReports.update((reports) => reports.filter((report) => report !== payload));
}
