import { derived } from 'svelte/store';
import { resumableUploads } from '../../modules/resumable-upload-store';
import { resumableDownloads } from '../../modules/resumable-download-store';
import { historyEvents, isUnfinishedTransfer, type TransferEvent } from './notif-store';
import { uploadActivities } from './upload-activity-store';

/** Journal jobs and full session batches own their compact legacy counterpart. */
export const visibleHistory = derived(
    [historyEvents, resumableUploads, resumableDownloads, uploadActivities],
    ([events, uploads, downloads, batches]) => {
        const uploadJobs = new Set(uploads.map((job) => job.jobId));
        const represented = new Set([
            ...downloads.map((job) => `xfer:down:file:${job.channelId}:${job.logicalMsgId}`),
            ...batches.map((batch) => batch.transferId).filter((id): id is string => Boolean(id)),
        ]);
        return events.filter((entry) => entry.kind !== 'transfer'
            || ((!entry.resumableJobId || !uploadJobs.has(entry.resumableJobId)) && !represented.has(entry.id)));
    },
);
export const visibleActiveTransfers = derived(visibleHistory, (events) => events.filter((entry): entry is TransferEvent => entry.kind === 'transfer' && isUnfinishedTransfer(entry.status)));
export const visibleRecentEvents = derived(visibleHistory, (events) => events.filter((entry) => entry.kind !== 'transfer' || !isUnfinishedTransfer(entry.status)));

/** A full batch exposes its own recovery controls in the detail view. */
export const standaloneResumableUploads = derived([resumableUploads, uploadActivities], ([jobs, batches]) => {
    const represented = new Set(batches.flatMap((batch) => batch.resumableJobIds ?? []));
    return jobs.filter((job) => !represented.has(job.jobId));
});
