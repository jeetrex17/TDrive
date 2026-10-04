import {
    DiscardResumableDownload as rawDiscardResumableDownload,
    ListResumableDownloads as rawListResumableDownloads,
    PauseResumableDownload as rawPauseResumableDownload,
    ResumeDownload as rawResumeDownload,
} from '../../bindings/TDrive/internal/app/app';
import type { DownloadResult, OperationResult } from '../types';
import { normalizeDownloadResult } from './files';
import { invokeBackend } from './gateway';
import { normalizeOperationResult, requireOperationSuccess } from './operation';
import { asRecord, nonNegativeNumber } from './shared';

export type ResumableDownloadStatus =
    | 'paused' | 'downloading' | 'waiting_network' | 'waiting_unlock'
    | 'needs_destination' | 'source_changed' | 'verifying' | 'saving'
    | 'completed' | 'error';

export interface ResumableDownload {
    jobId: string;
    channelId: number;
    logicalMsgId: number;
    name: string;
    status: ResumableDownloadStatus;
    verifiedBytes: number;
    totalBytes: number;
    error: string;
    savedPath: string;
    /** Current-session rate only; it is never read from durable storage. */
    speed: number;
}

const STATUSES: readonly string[] = [
    'paused', 'downloading', 'waiting_network', 'waiting_unlock',
    'needs_destination', 'source_changed', 'verifying', 'saving',
    'completed', 'error',
];

export function normalizeResumableDownload(value: unknown): ResumableDownload | null {
    const raw = asRecord(value);
    const jobId = typeof raw.job_id === 'string' ? raw.job_id.slice(0, 128) : '';
    const status = typeof raw.status === 'string' ? raw.status : '';
    const channelId = Number(raw.channel_id);
    const logicalMsgId = Number(raw.logical_msg_id);
    if (!jobId || !STATUSES.includes(status)
        || !Number.isSafeInteger(channelId) || channelId <= 0
        || !Number.isSafeInteger(logicalMsgId) || logicalMsgId <= 0) return null;
    const totalBytes = nonNegativeNumber(raw.total_bytes);
    return {
        jobId,
        channelId,
        logicalMsgId,
        name: String(raw.name ?? '').slice(0, 300),
        status: status as ResumableDownloadStatus,
        verifiedBytes: Math.min(totalBytes, nonNegativeNumber(raw.verified_bytes)),
        totalBytes,
        error: String(raw.error ?? '').slice(0, 300),
        savedPath: String(raw.saved_path ?? ''),
        speed: 0,
    };
}

export async function listResumableDownloads(channelId: number): Promise<ResumableDownload[]> {
    const raw = asRecord(await invokeBackend(rawListResumableDownloads, channelId));
    requireOperationSuccess(normalizeOperationResult(raw.result, 'Could not load downloads'));
    return Array.isArray(raw.jobs)
        ? raw.jobs.map(normalizeResumableDownload).filter((job): job is ResumableDownload => job !== null)
        : [];
}

export async function resumeDownload(channelId: number, jobId: string, requestId: string): Promise<DownloadResult> {
    return normalizeDownloadResult(await invokeBackend(rawResumeDownload, channelId, jobId, requestId));
}

export async function pauseResumableDownload(channelId: number, jobId: string): Promise<OperationResult> {
    return normalizeOperationResult(await invokeBackend(rawPauseResumableDownload, channelId, jobId), 'Could not pause download');
}

export async function discardResumableDownload(channelId: number, jobId: string): Promise<OperationResult> {
    return normalizeOperationResult(await invokeBackend(rawDiscardResumableDownload, channelId, jobId), 'Could not discard download');
}
