<script lang="ts">
    import FailedUploadReports from '../notifications/FailedUploadReports.svelte';
    import { failedUploadReports, dismissFailedUploadReport } from '../modals/failed-uploads-store';
    import { reviewFailedUploads } from '../../modules/transfers';
    import ArrowDownUpIcon from '@lucide/svelte/icons/arrow-down-up';
    import ChevronRightIcon from '@lucide/svelte/icons/chevron-right';
    import { type TransferEvent } from '../notifications/notif-store';
    import { visibleActiveTransfers, visibleRecentEvents, standaloneResumableUploads as resumableUploads } from '../notifications/visible-history';
    import { openActivity } from '../notifications/activity-store';
    import { uploadActivities, clearUploadActivityHistory } from '../notifications/upload-activity-store';
    import QueuedUploadRow from '../notifications/QueuedUploadRow.svelte';
    import { removeQueuedUpload } from '../../modules/upload-queue';
    import { sidebarState } from '../sidebar/sidebar-store';
    import EventRow from '../notifications/EventRow.svelte';
    import MobileTransferRow from './MobileTransferRow.svelte';
    import MobileResumableUploadRow from './MobileResumableUploadRow.svelte';
    import ResumableDownloadRow from '../notifications/ResumableDownloadRow.svelte';
    import { pauseUpload } from '../../modules/resumable-uploads';
    import { pauseDownload, resumableDownloads } from '../../modules/resumable-downloads';
    import { cancelSingleUpload, cancelTransfersInDirection, cancelUploadFile, clearHistory, parseTransferKey } from '../../modules/notif-bell';
    import { isPhotoBackupActivity } from '../../modules/photo-backup/activity';
    import { humanizeBackendError } from '../../modules/errors';
    import { notify } from '../../modules/notifications';
    import { downloadRetryFor } from '../../modules/transfers';
    import { shareFile } from '../../api';
    import { downloadSharePaths, forgetDownloadSharePath } from './mobile-shell-store';

    /** How many transfers are in flight; more than one earns a way to stop the lot. */
    const visibleActive = visibleActiveTransfers;
    const visibleRecent = $derived($visibleRecentEvents.filter((entry) => entry.kind === 'transfer' || entry.level === 'error' || entry.level === 'warning'));
    const queued = $derived($uploadActivities.filter((entry) => entry.status === 'queued').sort((a, b) => a.addedAt - b.addedAt));
    const hasActive = $derived($resumableUploads.length + $resumableDownloads.length + queued.length > 0 || $visibleActive.length > 0);
    const inFlight = $derived($visibleActive.length + queued.length + $resumableUploads.filter((job) => job.status === 'uploading').length + $resumableDownloads.filter((job) => job.status === 'downloading').length);

    /**
     * Stopping one row, where the backend can do that.
     *
     * An upload is numbered within its batch and can be stopped on its own. A
     * download can only be stopped while it is the job actually running -- the
     * queue behind it has no per-item cancel -- so a waiting row offers none
     * rather than a control that would quietly stop somebody else's transfer.
     * Cancel all covers those; see the section header.
     */
    function cancelFor(transfer: TransferEvent): (() => void) | undefined {
        // The backup queue is the backend's to schedule, not ours to interrupt.
        if (isPhotoBackupActivity(transfer.id)) return undefined;
        const key = parseTransferKey(transfer.id);
        // A numeric id is an upload's number within its batch, which is exactly
        // what the backend can stop on its own. Anything else -- an import, a
        // folder walk -- is one job under one row.
        const uploadId = key?.direction === 'up' ? Number(key.id) : Number.NaN;
        if (Number.isInteger(uploadId)) return () => cancelSingleUpload(uploadId);
        if (transfer.direction === 'up') return () => cancelTransfersInDirection('up');
        // "active" and not "running": a download still working out what it is
        // downloading is the job the backend has in hand, and it was the one row
        // on screen with no way to stop it.
        if (transfer.status === 'active') return () => cancelTransfersInDirection('down');
        return undefined;
    }

    /**
     * Three uploads run at a time, so twenty files show three rows and stopping
     * those three only lets the next three start. This is the way out of the
     * batch, and of a download queue whose waiting rows cannot be stopped alone.
     */
    function cancelEverything(): void {
        // Waiting batches go first, or stopping the running one starts the next.
        for (const batch of queued) removeQueuedUpload(batch.id);
        if ($visibleActive.some((transfer) => transfer.direction === 'up')) cancelTransfersInDirection('up');
        if ($visibleActive.some((transfer) => transfer.direction === 'down')) cancelTransfersInDirection('down');
        for (const job of $resumableUploads) {
            if (job.status === 'uploading') void pauseUpload(job.jobId);
        }
        for (const job of $resumableDownloads) {
            if (job.status === 'downloading') void pauseDownload(job.jobId);
        }
    }

    /** Re-queues a failed download, forgetting the sandbox file the last try left. */
    function retryFor(transfer: TransferEvent): (() => void) | undefined {
        const retry = downloadRetryFor(transfer);
        if (!retry) return undefined;
        return () => {
            forgetDownloadSharePath(transfer.id);
            retry();
        };
    }

    /**
     * Finished single-file downloads keep their sandbox path so the share sheet
     * can be opened again from here, long after the one that opened on arrival.
     */
    function shareFor(transferId: string): (() => void) | undefined {
        const path = $downloadSharePaths.get(transferId);
        if (!path) return undefined;
        return () => {
            void shareFile(path)
                .then((result) => {
                    if (!result.ok) {
                        notify({ level: 'error', title: 'Could not share', body: humanizeBackendError(result.error) });
                    }
                })
                .catch((error: unknown) => {
                    notify({ level: 'error', title: 'Could not share', body: humanizeBackendError(error) });
                });
        };
    }
</script>

<div class="mobile-scroll transfers-tab">
    {#if !hasActive && visibleRecent.length === 0 && $failedUploadReports.length === 0}
        <div class="mobile-empty">
            <span class="mobile-empty-glyph">
                <ArrowDownUpIcon size={40} strokeWidth={1.6} aria-hidden="true" />
            </span>
            <p class="mobile-empty-title">No transfers</p>
            <p class="mobile-empty-body">Uploads and downloads show up here.</p>
        </div>
    {:else}
        <FailedUploadReports variant="mobile" reports={$failedUploadReports} onReview={reviewFailedUploads} onDismiss={dismissFailedUploadReport} />
        {#if hasActive}
            <div class="transfers-section-head">
                <h2 class="mobile-section-label">Active</h2>
                {#if inFlight > 1}
                    <button type="button" class="transfers-clear" onclick={cancelEverything}>Stop all</button>
                {/if}
            </div>
            <div class="transfers-group" role="list">
                {#each $resumableUploads as job (job.jobId)}
                    <MobileResumableUploadRow {job} />
                {/each}
                {#each $resumableDownloads as job (job.jobId)}
                    <ResumableDownloadRow {job} mobile />
                {/each}
                {#each $visibleActive as transfer (transfer.id)}
                    <MobileTransferRow {transfer} onCancel={cancelFor(transfer)} onCancelFile={cancelUploadFile} />
                {/each}
                {#each queued as batch (batch.id)}
                    <QueuedUploadRow {batch} mobile activeDriveId={$sidebarState.activeChannelId} onOpen={() => openActivity({ selectedId: batch.id, restoreFocus: '#mobile-view-activity' })} />
                {/each}
            </div>
        {/if}
        {#if visibleRecent.length > 0}
            <div class="transfers-section-head">
                <h2 class="mobile-section-label">Recent</h2>
                <button type="button" class="transfers-clear" onclick={() => { clearHistory(); clearUploadActivityHistory(); }}>Clear</button>
            </div>
            <div class="transfers-group" role="list">
                {#each visibleRecent.slice(0, 50) as entry (entry.id)}
                    {#if entry.kind === 'transfer'}
                        <MobileTransferRow
                            transfer={entry}
                            onRetry={retryFor(entry)}
                            onShare={shareFor(entry.id)}
                        />
                    {:else}
                        <EventRow event={entry} listItem />
                    {/if}
                {/each}
            </div>
        {/if}
    {/if}
    <button id="mobile-view-activity" type="button" class="transfers-activity-link" onclick={() => openActivity({ restoreFocus: '#mobile-view-activity' })}>
        <span>View all activity</span><ChevronRightIcon size={18} strokeWidth={2} aria-hidden="true" />
    </button>
</div>

<style>
    .transfers-activity-link { display: flex; align-items: center; justify-content: space-between; width: 100%; min-height: var(--touch-target); margin-top: var(--space-5); padding: var(--space-3) var(--space-4); border: 0; border-radius: var(--radius-lg); background: var(--color-surface-1); color: var(--color-text-soft); font: inherit; font-size: var(--type-sm); text-align: left; cursor: pointer; }
    .transfers-activity-link :global(svg) { color: var(--color-text-muted); }
    .transfers-activity-link:focus-visible { outline: 2px solid var(--color-accent); outline-offset: 2px; }
    /* The card separates its own rows. A row cannot see its siblings -- each one
       is its own component -- and the separation is a property of the stack
       rather than of anything in it. */
    .transfers-group > :global(* + *) {
        border-top: 1px solid var(--color-border-soft);
    }

    /* Keep the quiet text treatment while giving the section actions an iOS
       and Android sized touch target. The pseudo-element extends the hit area
       without changing the heading rhythm. */
    .transfers-clear { position: relative; }
    .transfers-clear::after {
        content: '';
        position: absolute;
        inset: -8px;
    }
</style>
