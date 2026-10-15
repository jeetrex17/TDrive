import type { ResumableUpload } from '../../api/resumable-uploads';
import type { ResumableDownload } from '../../api/resumable-downloads';
import { isUnfinishedTransfer, type HistoryEvent, type TransferEvent } from './notif-store';
import type { UploadActivity } from './upload-activity-store';
import { transferDetailParts } from './transfer-view';

export type ActivityFilter = 'All' | 'Active' | 'Queued' | 'Needs attention' | 'History';
export const ACTIVITY_FILTERS: readonly ActivityFilter[] = ['All', 'Active', 'Queued', 'Needs attention', 'History'];
export type ActivityEntry =
    | { kind: 'batch'; id: string; name: string; status: ActivityFilter; summary: string; batch: UploadActivity; transfer?: TransferEvent }
    | { kind: 'transfer'; id: string; name: string; status: ActivityFilter; summary: string; transfer: TransferEvent }
    | { kind: 'upload'; id: string; name: string; status: ActivityFilter; summary: string; job: ResumableUpload }
    | { kind: 'download'; id: string; name: string; status: ActivityFilter; summary: string; job: ResumableDownload };

export function batchLabel(batch: UploadActivity, jobs: readonly ResumableUpload[] = []): string {
    const linked = jobs.filter((job) => batch.resumableJobIds?.includes(job.jobId));
    if (batch.status !== 'active' && batch.status !== 'queued' && linked.length > 0) {
        if (linked.some((job) => job.status === 'uploading')) return 'Resuming upload';
        if (linked.some((job) => job.status === 'canceling')) return 'Discarding upload';
        return 'Upload recovery needs attention';
    }
    return batch.status === 'queued' ? 'Waiting its turn'
        : batch.status === 'active' ? 'Uploading'
        : batch.status === 'done' ? 'Completed'
        : batch.status === 'failed' ? 'Needs attention' : 'Canceled';
}

export function batchActivityFilter(batch: UploadActivity, jobs: readonly ResumableUpload[]): ActivityFilter {
    if (batch.status === 'queued') return 'Queued';
    if (batch.status === 'active') return 'Active';
    const linked = jobs.filter((job) => batch.resumableJobIds?.includes(job.jobId));
    if (linked.some((job) => job.status === 'uploading' || job.status === 'canceling')) return 'Active';
    if (linked.length || batch.status === 'failed') return 'Needs attention';
    return 'History';
}

export function activityEntries(batches: readonly UploadActivity[], events: readonly HistoryEvent[], uploads: readonly ResumableUpload[], downloads: readonly ResumableDownload[], allEvents: readonly HistoryEvent[] = events): ActivityEntry[] {
    const entries: ActivityEntry[] = batches.map((batch) => ({
        kind: 'batch', id: batch.id, name: batch.name,
        status: batchActivityFilter(batch, uploads),
        summary: `${batchLabel(batch, uploads)} · ${batch.fileCount} ${batch.fileCount === 1 ? 'file' : 'files'}`,
        batch, transfer: allEvents.find((event): event is TransferEvent => event.kind === 'transfer' && event.id === batch.transferId),
    }));
    const representedJobs = new Set(batches.flatMap((batch) => batch.resumableJobIds ?? []));
    for (const job of uploads.filter((job) => !representedJobs.has(job.jobId))) entries.push({
        kind: 'upload', id: `upload:${job.jobId}`, name: job.name || 'Large upload',
        status: job.status === 'uploading' || job.status === 'canceling' ? 'Active' : 'Needs attention',
        summary: job.status.replace(/_/g, ' '), job,
    });
    for (const job of downloads) entries.push({
        kind: 'download', id: `download:${job.jobId}`, name: job.name || 'Download',
        status: ['downloading', 'verifying', 'saving'].includes(job.status) ? 'Active' : 'Needs attention',
        summary: job.status.replace(/_/g, ' '), job,
    });
    for (const transfer of events) {
        if (transfer.kind !== 'transfer') continue;
        entries.push({
            kind: 'transfer', id: transfer.id, name: transfer.name || (transfer.direction === 'up' ? 'Upload' : 'Download'),
            status: transfer.status === 'queued' ? 'Queued' : transfer.status === 'failed' ? 'Needs attention' : isUnfinishedTransfer(transfer.status) ? 'Active' : 'History',
            summary: transferDetailParts(transfer).map((part) => part.text).join(' · '), transfer,
        });
    }
    return entries;
}

/** Batch file names remain searchable even when they are outside the DOM page. */
export function matchesActivity(entry: ActivityEntry, query: string): boolean {
    const text = query.trim().toLocaleLowerCase();
    if (!text) return true;
    if (`${entry.name} ${entry.summary}`.toLocaleLowerCase().includes(text)) return true;
    return entry.kind === 'batch' && (entry.batch.files.some((file) => file.name.toLocaleLowerCase().includes(text))
        || `${entry.batch.driveTitle} ${entry.batch.destination}`.toLocaleLowerCase().includes(text));
}

export function activityTimestamp(timestamp?: number): string {
    return timestamp && timestamp > 0 ? new Date(timestamp).toLocaleString() : 'Not recorded';
}
