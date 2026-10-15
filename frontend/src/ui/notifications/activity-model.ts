import type { ResumableUpload } from '../../api/resumable-uploads';
import type { ResumableDownload } from '../../api/resumable-downloads';
import { isUnfinishedTransfer, type HistoryEvent, type TransferEvent } from './notif-store';
import type { UploadActivity, UploadActivityFile } from './upload-activity-store';
import { formatAge, transferDetailParts, transferPercent } from './transfer-view';

export type ActivityFilter = 'All' | 'Active' | 'Queued' | 'Needs attention' | 'History';
export const ACTIVITY_FILTERS: readonly ActivityFilter[] = ['All', 'Active', 'Queued', 'Needs attention', 'History'];
interface EntryBase { id: string; name: string; status: ActivityFilter; summary: string; at: number }
export type ActivityEntry =
    | EntryBase & { kind: 'batch'; batch: UploadActivity; transfer?: TransferEvent }
    | EntryBase & { kind: 'transfer'; transfer: TransferEvent }
    | EntryBase & { kind: 'upload'; job: ResumableUpload }
    | EntryBase & { kind: 'download'; job: ResumableDownload };

export function fileCountLabel(count: number): string {
    return `${count.toLocaleString()} ${count === 1 ? 'file' : 'files'}`;
}

/** A queued batch only runs while its drive is open; the backend rejects any other. */
export function isBatchPaused(batch: UploadActivity, activeDriveId: number | null | undefined): boolean {
    return batch.status === 'queued' && typeof activeDriveId === 'number' && batch.driveId !== activeDriveId;
}

export function batchLabel(batch: UploadActivity, jobs: readonly ResumableUpload[] = [], activeDriveId?: number | null): string {
    const linked = jobs.filter((job) => batch.resumableJobIds?.includes(job.jobId));
    if (batch.status !== 'active' && batch.status !== 'queued' && linked.length > 0) {
        if (linked.some((job) => job.status === 'uploading')) return 'Resuming upload';
        if (linked.some((job) => job.status === 'canceling')) return 'Discarding upload';
        return 'Upload recovery needs attention';
    }
    if (isBatchPaused(batch, activeDriveId)) return `Paused until ${batch.driveTitle} is open`;
    return batch.status === 'queued' ? 'Queued'
        : batch.status === 'active' ? 'Uploading'
        : batch.status === 'done' ? 'Completed'
        : batch.status === 'failed' ? 'Needs attention' : 'Canceled';
}

/** The list line: the state first, then the one figure worth reading beside it. */
export function batchSummary(batch: UploadActivity, jobs: readonly ResumableUpload[] = [], activeDriveId?: number | null, transfer?: TransferEvent, now = Date.now()): string {
    const label = batchLabel(batch, jobs, activeDriveId);
    const failed = batch.files.filter((file) => file.status === 'failed').length;
    const percent = batch.status === 'active' && transfer ? transferPercent(transfer) : null;
    const figure = percent !== null ? `${Math.round(percent)}%`
        : batch.status === 'failed' && failed > 0 ? `${failed} of ${fileCountLabel(batch.fileCount)} failed`
        : fileCountLabel(batch.fileCount);
    const age = batch.status === 'queued' || batch.status === 'active' ? '' : formatAge(batch.finishedAt, now);
    return [label, figure, age].filter(Boolean).join(' · ');
}

export function batchActivityFilter(batch: UploadActivity, jobs: readonly ResumableUpload[]): ActivityFilter {
    if (batch.status === 'queued') return 'Queued';
    if (batch.status === 'active') return 'Active';
    const linked = jobs.filter((job) => batch.resumableJobIds?.includes(job.jobId));
    if (linked.some((job) => job.status === 'uploading' || job.status === 'canceling')) return 'Active';
    if (linked.length || batch.status === 'failed') return 'Needs attention';
    return 'History';
}

const FILTER_RANK: Record<ActivityFilter, number> = { Active: 0, Queued: 1, 'Needs attention': 2, History: 3, All: 4 };

/**
 * Running work first, then the queue in the order it will run, then what needs
 * a decision, then history newest first.
 */
function compareEntries(a: ActivityEntry, b: ActivityEntry): number {
    const rank = FILTER_RANK[a.status] - FILTER_RANK[b.status];
    if (rank) return rank;
    return a.status === 'Queued' ? a.at - b.at : b.at - a.at;
}

export function activityEntries(batches: readonly UploadActivity[], events: readonly HistoryEvent[], uploads: readonly ResumableUpload[], downloads: readonly ResumableDownload[], options: { allEvents?: readonly HistoryEvent[]; activeDriveId?: number | null; now?: number } = {}): ActivityEntry[] {
    const { allEvents = events, activeDriveId, now = Date.now() } = options;
    const entries: ActivityEntry[] = batches.map((batch) => {
        const transfer = allEvents.find((event): event is TransferEvent => event.kind === 'transfer' && event.id === batch.transferId);
        return {
            kind: 'batch', id: batch.id, name: batch.name,
            status: batchActivityFilter(batch, uploads),
            summary: batchSummary(batch, uploads, activeDriveId, transfer, now),
            at: batch.status === 'queued' ? batch.addedAt : batch.finishedAt || batch.startedAt || batch.addedAt,
            batch, transfer,
        };
    });
    const representedJobs = new Set(batches.flatMap((batch) => batch.resumableJobIds ?? []));
    // The batch owns its aggregate row: one entry with full file detail, not two.
    const batchTransfers = new Set(batches.map((batch) => batch.transferId).filter(Boolean));
    for (const job of uploads.filter((job) => !representedJobs.has(job.jobId))) entries.push({
        kind: 'upload', id: `upload:${job.jobId}`, name: job.name || 'Large upload',
        status: job.status === 'uploading' || job.status === 'canceling' ? 'Active' : 'Needs attention',
        summary: job.status.replace(/_/g, ' '), at: now, job,
    });
    for (const job of downloads) entries.push({
        kind: 'download', id: `download:${job.jobId}`, name: job.name || 'Download',
        status: ['downloading', 'verifying', 'saving'].includes(job.status) ? 'Active' : 'Needs attention',
        summary: job.status.replace(/_/g, ' '), at: now, job,
    });
    for (const transfer of events) {
        if (transfer.kind !== 'transfer' || batchTransfers.has(transfer.id)) continue;
        entries.push({
            kind: 'transfer', id: transfer.id, name: transfer.name || (transfer.direction === 'up' ? 'Upload' : 'Download'),
            status: transfer.status === 'queued' ? 'Queued' : transfer.status === 'failed' ? 'Needs attention' : isUnfinishedTransfer(transfer.status) ? 'Active' : 'History',
            summary: transferDetailParts(transfer, now).map((part) => part.text).join(' · '),
            at: transfer.finishedAt || transfer.startedAt, transfer,
        });
    }
    return entries.sort(compareEntries);
}

export interface FileFolder { folder: string; files: UploadActivityFile[] }

/**
 * A batch's files by the folder they sit in, in the order the upload reached
 * them: loose files first, then each folder of the selection.
 */
export function groupFilesByFolder(files: readonly UploadActivityFile[]): FileFolder[] {
    const groups = new Map<string, UploadActivityFile[]>([['', []]]);
    for (const file of [...files].sort((a, b) => a.index - b.index)) {
        const path = file.path ?? '';
        const folder = path.includes('/') ? path.slice(0, path.lastIndexOf('/')) : '';
        const group = groups.get(folder);
        if (group) group.push(file);
        else groups.set(folder, [file]);
    }
    return [...groups].filter(([, members]) => members.length).map(([folder, members]) => ({ folder, files: members }));
}

/** Batch file names remain searchable even when they are outside the DOM page. */
export function matchesActivity(entry: ActivityEntry, query: string): boolean {
    const text = query.trim().toLocaleLowerCase();
    if (!text) return true;
    if (`${entry.name} ${entry.summary}`.toLocaleLowerCase().includes(text)) return true;
    return entry.kind === 'batch' && (entry.batch.files.some((file) => (file.path ?? file.name).toLocaleLowerCase().includes(text))
        || `${entry.batch.driveTitle} ${entry.batch.destination}`.toLocaleLowerCase().includes(text));
}

export function activityTimestamp(timestamp?: number): string {
    return timestamp && timestamp > 0
        ? new Date(timestamp).toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' })
        : 'Not recorded';
}
