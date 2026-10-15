<script lang="ts">
    import { readable } from 'svelte/store';
    import { resumableUploads } from '../../modules/resumable-upload-store';
    import { removeQueuedUpload } from '../../modules/upload-queue';
    import { cancelTransfersInDirection, cancelUploadFile } from '../../modules/notif-bell';
    import { downloadRetryFor } from '../../modules/transfers';
    import { formatBytes } from '../../utils';
    import type { ActivityEntry } from './activity-model';
    import { activityTimestamp, batchLabel } from './activity-model';
    import { formatEta, formatSizePair, etaSeconds, transferredBytes } from './transfer-view';
    import TransferRow from './TransferRow.svelte';
    import ResumableUploadRow from './ResumableUploadRow.svelte';
    import ResumableDownloadRow from './ResumableDownloadRow.svelte';
    import ActivityMessage from './ActivityMessage.svelte';

    interface Props { entry: ActivityEntry; queuePosition?: number }
    let { entry, queuePosition = 0 }: Props = $props();
    let filePage = $state(0);
    const PAGE_SIZE = 50;
    const batch = $derived(entry.kind === 'batch' ? entry.batch : null);
    const transfer = $derived(entry.kind === 'transfer' || entry.kind === 'batch' ? entry.transfer : undefined);
    const pageCount = $derived(batch ? Math.max(1, Math.ceil(batch.files.length / PAGE_SIZE)) : 1);
    const currentPage = $derived(Math.min(filePage, pageCount - 1));
    const activeFiles = $derived(new Map((transfer?.items ?? []).map((item) => [item.key, item])));
    const files = $derived(batch?.files.slice(currentPage * PAGE_SIZE, (currentPage + 1) * PAGE_SIZE) ?? []);
    const linkedJobs = $derived(batch ? $resumableUploads.filter((job) => batch.resumableJobIds?.includes(job.jobId)) : []);
    const showTransfer = $derived(transfer && (linkedJobs.length === 0 || batch?.status === 'active'));
    const destinationPath = $derived(batch && batch.destination !== batch.driveTitle ? batch.destination.replace(`${batch.driveTitle} / `, '') : '');
    const clock = readable(Date.now(), (set) => {
        set(Date.now());
        const timer = setInterval(() => set(Date.now()), 1000);
        return () => clearInterval(timer);
    });
    const counts = $derived(batch?.files.length ? {
        done: batch.files.filter((file) => file.status === 'done').length,
        failed: batch.files.filter((file) => file.status === 'failed').length,
        canceled: batch.files.filter((file) => file.status === 'canceled').length,
        active: batch.files.filter((file) => file.status === 'active').length,
        queued: batch.files.filter((file) => file.status === 'queued').length,
    } : null);
    const eta = $derived(transfer ? etaSeconds(transfer, $clock) : null);
    const elapsed = $derived(transfer && transfer.status !== 'queued' && transfer.startedAt > 0
        ? Math.max(0, Math.round(((transfer.finishedAt || $clock) - transfer.startedAt) / 1000)) : null);
    const retry = $derived(entry.kind === 'transfer' ? downloadRetryFor(entry.transfer) : undefined);
</script>

<section class="activity-details" aria-label={`Details for ${entry.name}`}>
    <div class="detail-heading">
        <span class="detail-eyebrow">{entry.kind === 'download' || (transfer?.direction === 'down') ? 'Download' : 'Upload'}</span>
        <h3>{entry.name}</h3>
        <p class="detail-status" data-status={entry.status}>{batch ? batchLabel(batch, $resumableUploads) : entry.summary}</p>
    </div>

    {#if entry.kind === 'upload'}
        <ResumableUploadRow job={entry.job} />
        <dl><div><dt>Drive channel</dt><dd>{entry.job.channelId}</dd></div><div><dt>Recovery</dt><dd>Saved upload journal</dd></div><div><dt>Confirmed size</dt><dd>{formatSizePair(entry.job.confirmedBytes, entry.job.size)}</dd></div></dl>
        <ActivityMessage title={entry.name} message={entry.job.error} />
    {:else if entry.kind === 'download'}
        <ResumableDownloadRow job={entry.job} />
        <dl><div><dt>Drive channel</dt><dd>{entry.job.channelId}</dd></div><div><dt>Recovery</dt><dd>Saved download journal</dd></div><div><dt>Verified size</dt><dd>{formatSizePair(entry.job.verifiedBytes, entry.job.totalBytes)}</dd></div>
            {#if entry.job.savedPath}<div><dt>Saved to</dt><dd>{entry.job.savedPath}</dd></div>{/if}
        </dl>
        <ActivityMessage title={entry.name} message={entry.job.error} />
    {:else}
        {#if showTransfer && transfer}
            <TransferRow {transfer} onCancel={cancelTransfersInDirection} onCancelFile={cancelUploadFile} />
            <dl>
                <div><dt>Transferred</dt><dd>{transfer.total > 0 ? formatSizePair(transferredBytes(transfer), transfer.total) : transfer.bytes > 0 ? `${formatBytes(transfer.bytes)} so far` : 'Not available yet'}</dd></div>
                {#if transfer.status === 'active' && transfer.speed > 0}<div><dt>Speed</dt><dd>{formatBytes(transfer.speed)}/s</dd></div>{/if}
                {#if eta !== null}<div><dt>Estimated time</dt><dd>{formatEta(eta)}</dd></div>{/if}
                {#if elapsed !== null}<div><dt>Elapsed</dt><dd>{elapsed < 60 ? `${elapsed}s` : `${Math.floor(elapsed / 60)} min ${elapsed % 60}s`}</dd></div>{/if}
            </dl>
        {/if}
        {#if batch}
            <dl>
                <div><dt>Destination</dt><dd>{batch.driveTitle}{#if destinationPath}<span class="destination-path">{destinationPath}</span>{/if}</dd></div>
                <div><dt>Encryption</dt><dd>{batch.encrypt ? 'Encrypted' : 'Not encrypted'}</dd></div>
                {#if batch.status === 'queued'}<div><dt>Queue position</dt><dd>{queuePosition || 1} · Waiting for earlier uploads</dd></div>{/if}
            </dl>
            {#if batch.status === 'queued'}
                <p class="detail-note">Keep TDrive open. Queued selections are available for this session.</p>
                <button type="button" class="detail-action danger" onclick={() => removeQueuedUpload(batch.id)}>Remove from queue</button>
            {:else if batch.status === 'active' && !transfer}
                <button type="button" class="detail-action danger" onclick={() => cancelTransfersInDirection('up')}>Cancel all uploads</button>
            {/if}
            {#if batch.detailNote}<p class="detail-note">{batch.detailNote}</p>{/if}
            <ActivityMessage title={entry.name} message={batch.error || ''} />
            {#if linkedJobs.length > 0}
                <section class="files-section" aria-label="Upload recovery"><h4>Upload recovery</h4><p class="detail-note">Recovery controls show the latest confirmed state. File outcomes below describe the initial attempt.</p>{#each linkedJobs as job (job.jobId)}<ResumableUploadRow {job} />{/each}</section>
            {/if}
                <section class="files-section" aria-label="Files in upload">
                    <h4>Files <span>{batch.fileCount}</span></h4>
                    {#if counts}<p class="file-summary">{counts.done} succeeded · {counts.failed} failed · {counts.canceled} canceled · {counts.active} uploading · {counts.queued} waiting</p>{/if}
                    {#if batch.files.length > 0}
                        <ul class="file-list">
                            {#each files as file (file.index)}
                                {@const activeFile = activeFiles.get(String(file.index))}
                                <li data-activity-file>
                                    <span class="file-name">{file.name}</span>
                                    <span class="file-status" class:failed={file.status === 'failed'}>{file.status === 'done' ? 'Completed' : file.status === 'active' ? 'Uploading' : file.status === 'queued' ? 'Waiting' : file.status === 'failed' ? 'Failed' : 'Canceled'}{file.status === 'active' && activeFile ? ` · ${Math.round(activeFile.progress)}%` : ''}</span>
                                    {#if file.size !== undefined}<span class="file-size">{formatBytes(file.size)}</span>{/if}
                                    {#if file.message}<p class="file-message">{file.message}</p>{/if}
                                </li>
                            {/each}
                        </ul>
                        {#if pageCount > 1}
                            <div class="file-pagination" aria-label="File pages">
                                <button type="button" disabled={currentPage === 0} onclick={() => filePage = currentPage - 1}>Previous files</button>
                                <span>{currentPage * PAGE_SIZE + 1}–{Math.min((currentPage + 1) * PAGE_SIZE, batch.files.length)} of {batch.files.length}</span>
                                <button type="button" disabled={currentPage >= pageCount - 1} onclick={() => filePage = currentPage + 1}>Next files</button>
                            </div>
                        {/if}
                    {:else}
                        <p class="detail-note">Individual file records are not available for this operation.</p>
                    {/if}
                </section>
        {:else if transfer}
            <ActivityMessage title={entry.name} message={transfer.note || ''} />
            {#if retry}<button type="button" class="detail-action" onclick={retry}>Retry download</button>{/if}
            <p class="detail-note">This transfer has a compact history record. Destination, encryption and completed file details were not recorded.</p>
        {/if}
        <section class="timeline" aria-label="Timeline">
            <h4>Timeline</h4>
            <dl>
                {#if batch}<div><dt>Added</dt><dd>{activityTimestamp(batch.addedAt)}</dd></div>{/if}
                <div><dt>Started</dt><dd>{activityTimestamp(batch?.startedAt || transfer?.startedAt)}</dd></div>
                {#if batch?.finishedAt || transfer?.finishedAt}<div><dt>Finished</dt><dd>{activityTimestamp(batch?.finishedAt || transfer?.finishedAt)}</dd></div>{/if}
            </dl>
        </section>
    {/if}
</section>

<style>
    .activity-details { min-width: 0; padding: var(--space-6); }
    .detail-heading { margin-bottom: var(--space-5); }
    .detail-eyebrow { color: var(--color-text-muted); font-size: var(--type-xs); }
    h3 { margin: var(--space-1) 0 var(--space-2); font-size: var(--type-xl); font-weight: var(--weight-semibold); overflow-wrap: anywhere; }
    .detail-status { margin: 0; color: var(--color-text-muted); font-size: var(--type-sm); }
    [data-status='Needs attention'], .failed { color: var(--color-danger); }
    dl { margin: var(--space-5) 0; display: grid; gap: var(--space-3); }
    dl > div { display: grid; grid-template-columns: 120px minmax(0, 1fr); gap: var(--space-3); font-size: var(--type-sm); }
    dt { color: var(--color-text-muted); }
    dd { margin: 0; overflow-wrap: anywhere; }
    .destination-path { display: block; margin-top: var(--space-1); color: var(--color-text-muted); }
    .detail-note, .file-summary { color: var(--color-text-muted); font-size: var(--type-xs); line-height: var(--leading-normal); }
    h4 { margin: 0 0 var(--space-2); font-size: var(--type-base); font-weight: var(--weight-semibold); }
    h4 span { margin-left: var(--space-1); color: var(--color-text-muted); font-weight: var(--weight-regular); }
    .files-section, .timeline { margin-top: var(--space-6); padding-top: var(--space-5); border-top: 1px solid var(--color-border-soft); }
    .timeline dl { margin-bottom: 0; }
    .file-list { margin: var(--space-3) 0; padding: 0; list-style: none; }
    .file-list li { display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: var(--space-1) var(--space-3); padding: var(--space-3) 0; border-bottom: 1px solid var(--color-border-soft); }
    .file-name { font-size: var(--type-sm); overflow-wrap: anywhere; }
    .file-status, .file-size { font-size: var(--type-xs); color: var(--color-text-muted); }
    .file-message { margin: var(--space-1) 0 0; grid-column: 1 / -1; color: var(--color-danger); font-size: var(--type-xs); overflow-wrap: anywhere; user-select: text; }
    .detail-action, .file-pagination button { border: 1px solid var(--color-border-soft); border-radius: var(--radius-sm); padding: var(--space-2) var(--space-3); background: var(--color-surface-1); color: var(--color-text-soft); font: inherit; font-size: var(--type-xs); cursor: pointer; }
    .detail-action.danger { color: var(--color-danger); }
    button:focus-visible { outline: 2px solid var(--color-accent); outline-offset: 2px; }
    button:disabled { opacity: .45; cursor: default; }
    .file-pagination { display: flex; align-items: center; justify-content: space-between; gap: var(--space-2); font-size: var(--type-xs); color: var(--color-text-muted); }
    @media (max-width: 700px) { .activity-details { padding: var(--space-4); } dl > div { grid-template-columns: 100px minmax(0, 1fr); } button { min-height: var(--touch-target); } .activity-details :global(.actions button) { min-height: var(--touch-target); } .activity-details :global(.notif-row-cancel) { min-width: var(--touch-target); min-height: var(--touch-target); } .file-pagination { flex-wrap: wrap; } }
</style>
