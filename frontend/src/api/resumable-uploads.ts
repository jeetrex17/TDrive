import {
    CancelResumableUpload as rawCancelResumableUpload,
    ListResumableUploads as rawListResumableUploads,
    PauseResumableUpload as rawPauseResumableUpload,
    ResumeResumableUpload as rawResumeResumableUpload,
} from "../../bindings/TDrive/app";
import type { OperationResult, UploadResult } from "../types";
import type { FileMetaData } from "../../bindings/TDrive/backend/models";
import { toFileItem } from "./files";
import { invokeBackend } from "./gateway";
import { normalizeOperationResult } from "./operation";
import { asRecord, nonNegativeNumber } from "./shared";

/** The backend journal, rather than the webview's transfer history, owns these bytes. */
export interface ResumableUpload {
    jobId: string;
    channelId: number;
    name: string;
    size: number;
    confirmedBytes: number;
    status: ResumableUploadStatus;
    error: string;
}

export type ResumableUploadStatus = 'paused' | 'uploading' | 'needs_source' | 'uncertain' | 'uncertain_manifest' | 'restart_required' | 'completed' | 'canceling';

const STATUSES: readonly string[] = [
    'paused', 'uploading', 'needs_source', 'uncertain', 'uncertain_manifest', 'restart_required', 'completed', 'canceling',
];

export function normalizeResumableUpload(value: unknown): ResumableUpload | null {
    const raw = asRecord(value);
    const jobId = typeof raw.job_id === "string" ? raw.job_id.slice(0, 128) : "";
    const status = typeof raw.status === "string" ? raw.status : "";
    if (!jobId || !STATUSES.includes(status)
        || typeof raw.channel_id !== "number" || !Number.isSafeInteger(raw.channel_id) || raw.channel_id <= 0) return null;
    const size = nonNegativeNumber(raw.size);
    return {
        jobId,
        channelId: Number(raw.channel_id),
        name: String(raw.name ?? "").slice(0, 300),
        size,
        confirmedBytes: Math.min(size, nonNegativeNumber(raw.confirmed_bytes)),
        status: status as ResumableUploadStatus,
        error: String(raw.error ?? "").slice(0, 300),
    };
}

export async function listResumableUploads(): Promise<ResumableUpload[]> {
    const raw = await invokeBackend(rawListResumableUploads);
    if (!Array.isArray(raw)) return [];
    return raw.map(normalizeResumableUpload).filter((job): job is ResumableUpload => job !== null);
}

export async function resumeResumableUpload(jobId: string, sourcePath = ""): Promise<UploadResult> {
    const raw = asRecord(await invokeBackend(rawResumeResumableUpload, jobId, sourcePath));
    return {
        result: normalizeOperationResult(raw.result, "Could not resume upload"),
        files: Array.isArray(raw.files) ? (raw.files as FileMetaData[]).map(toFileItem) : [],
    };
}

export async function pauseResumableUpload(jobId: string): Promise<OperationResult> {
    return normalizeOperationResult(await invokeBackend(rawPauseResumableUpload, jobId), "Could not pause upload");
}

export async function cancelResumableUpload(jobId: string): Promise<OperationResult> {
    return normalizeOperationResult(await invokeBackend(rawCancelResumableUpload, jobId), "Could not discard upload");
}
