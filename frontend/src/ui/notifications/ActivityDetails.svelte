<script lang="ts">
    import { readable } from 'svelte/store';
    import { resumableUploads } from '../../modules/resumable-upload-store';
    import { removeQueuedUpload } from '../../modules/upload-queue';
    import { cancelTransfersInDirection, cancelUploadFile } from '../../modules/notif-bell';
    import { downloadRetryFor, reviewFailedUploads } from '../../modules/transfers';
    import { switchActiveChannel } from '../../modules/channels';
    import { formatBytes } from '../../utils';
    import { failedUploadReports } from '../modals/failed-uploads-store';
    import { sidebarState } from '../sidebar/sidebar-store';
    import ChevronRightIcon from '@lucide/svelte/icons/chevron-right';
    import FolderIcon from '@lucide/svelte/icons/folder';
    import type { UploadActivityFile } from './upload-activity-store';
    import type { ActivityEntry } from './activity-model';
    import { activityTimestamp, batchLabel, fileCountLabel, groupFilesByFolder, isBatchPaused } from './activity-model';
    import { formatSizePair } from './transfer-view';
    import TransferRow from './TransferRow.svelte';
    import ResumableUploadRow from './ResumableUploadRow.svelte';
    import ResumableDownloadRow from './ResumableDownloadRow.svelte';
    import ActivityMessage from './ActivityMessage.svelte';

    interface Props { entry: ActivityEntry; activeDriveId?: number | null; queuedAhead?: number; uploadRunning?: boolean }
    let { entry, activeDriveId, queuedAhead = 0, uploadRunning = false }: Props = $props();
    let filePage = $state(0);
    let collapsed = $state<string[]>([]);
    const PAGE_SIZE = 50;
    const FILE_STATUS = { done: 'Completed', active: 'Uploading', queued: 'Waiting', failed: 'Failed', canceled: 'Canceled' } as const;
    const batch = $derived(entry.kind === 'batch' ? entry.batch : null);
    const transfer = $derived(entry.kind === 'transfer' || entry.kind === 'batch' ? entry.transfer : undefined);
    const activeFiles = $derived(new Map((transfer?.items ?? []).map((item) => [item.key, item])));
    // Loose files as they are, then one heading per folder with its files under
    // it. A folded folder keeps its heading and counts.
    type FileRow =
        | { kind: 'folder'; folder: string; total: number; done: number; failed: number; active: number; open: boolean }
        | { kind: 'file'; file: UploadActivityFile; nested: boolean };
    const rows = $derived.by(() => {
        const out: FileRow[] = [];
        for (const { folder, files } of groupFilesByFolder(batch?.files ?? [])) {
            const open = !collapsed.includes(folder);
            if (folder) {
                const count = (status: UploadActivityFile['status']) => files.filter((file) => file.status === status).length;
                out.push({ kind: 'folder', folder, total: files.length, done: count('done'), failed: count('failed'), active: count('active'), open });
            }
            if (open) for (const file of files) out.push({ kind: 'file', file, nested: Boolean(folder) });
        }
        return out;
    });
    const pageCount = $derived(Math.max(1, Math.ceil(rows.length / PAGE_SIZE)));
    const currentPage = $derived(Math.min(filePage, pageCount - 1));
    const pageRows = $derived(rows.slice(currentPage * PAGE_SIZE, (currentPage + 1) * PAGE_SIZE));
    const linkedJobs = $derived(batch ? $resumableUploads.filter((job) => batch.resumableJobIds?.includes(job.jobId)) : []);
    const showTransfer = $derived(transfer && (linkedJobs.length === 0 || batch?.status === 'active'));
    const destinationPath = $derived(batch && batch.destination !== batch.driveTitle ? batch.destination.replace(`${batch.driveTitle} / `, '') : '');
    const paused = $derived(batch ? isBatchPaused(batch, activeDriveId) : false);
    const report = $derived(batch ? $failedUploadReports.find((candidate) => candidate.activityId === batch.id) : undefined);
    const jobDrive = $derived(entry.kind === 'upload' || entry.kind === 'download'
        ? [...$sidebarState.personal, ...$sidebarState.shared].find((drive) => drive.id === entry.job.channelId)?.title : undefined);
    const clock = readable(Date.now(), (set) => {
        const timer = setInterval(() => set(Date.now()), 1000);
        return () => clearInterval(timer);
    });
    const fileSummary = $derived.by(() => {
        if (!batch?.files.length) return '';
        const count = (status: keyof typeof FILE_STATUS) => batch.files.filter((file) => file.status === status).length;
        // An import names its files as it reaches them; the rest still count as waiting.
        const unlisted = batch.status === 'active' || batch.status === 'queued' ? Math.max(0, batch.fileCount - batch.files.length) : 0;
        return [
            [count('done'), 'completed'], [count('failed'), 'failed'], [count('canceled'), 'canceled'],
            [count('active'), 'uploading'], [count('queued') + unlisted, 'waiting'],
        ].filter(([value]) => value).map(([value, label]) => `${value.toLocaleString()} ${label}`).join(' · ');
    });
    const startedAt = $derived(batch?.startedAt || transfer?.startedAt || 0);
    const finishedAt = $derived(batch?.finishedAt || transfer?.finishedAt || 0);
    const elapsed = $derived(startedAt > 0 && (finishedAt || batch?.status === 'active' || transfer?.status === 'active')
        ? Math.max(0, Math.round(((finishedAt || $clock) - startedAt) / 1000)) : null);
    const status = $derived.by(() => {
        if (!batch) return entry.summary;
        if (batch.status !== 'queued' || paused) return batchLabel(batch, $resumableUploads, activeDriveId);
        if (queuedAhead > 0) return `Queued · ${queuedAhead} ${queuedAhead === 1 ? 'upload' : 'uploads'} ahead`;
        return uploadRunning ? 'Queued · Starts after the current upload' : 'Queued · Starting soon';
    });
    const retry = $derived(entry.kind === 'transfer' ? downloadRetryFor(entry.transfer) : undefined);

    function toggleFolder(folder: string): void {
        collapsed = collapsed.includes(folder) ? collapsed.filter((entry) => entry !== folder) : [...collapsed, folder];
    }
    function folderCounts(row: Extract<FileRow, { kind: 'folder' }>): string {
        return [fileCountLabel(row.total), row.done ? `${row.done} done` : '', row.active ? `${row.active} uploading` : '']
            .filter(Boolean).join(' · ');
    }

    function duration(seconds: number): string {
        if (seconds < 60) return `${seconds}s`;
        const minutes = Math.floor(seconds / 60);
        if (minutes < 60) return `${minutes} min ${seconds % 60}s`;
        return `${Math.floor(minutes / 60)} hr ${minutes % 60} min`;
    }
</script>

<section class="activity-details" aria-label={`Details for ${entry.name}`}>
    <div class="detail-heading">
        <span class="detail-eyebrow">{entry.kind === 'download' || (transfer?.direction === 'down') ? 'Download' : 'Upload'}</span>
        <h3>{entry.name}</h3>
        <p class="detail-status" data-tone={paused ? 'warning' : entry.status === 'Needs attention' ? 'danger' : undefined}>{status}</p>
        {#if batch?.status === 'queued' || report}
            <div class="detail-actions">
                {#if paused && batch}<button type="button" class="detail-action primary" onclick={() => void switchActiveChannel(batch.driveId)}>Open {batch.driveTitle}</button>{/if}
                {#if report}<button type="button" class="detail-action primary" onclick={() => reviewFailedUploads(report)}>Review failed files</button>{/if}
                {#if batch?.status === 'queued'}<button type="button" class="detail-action danger" onclick={() => removeQueuedUpload(batch.id)}>Remove from queue</button>{/if}
            </div>
            {#if batch?.status === 'queued'}<p class="detail-note">Keep TDrive open. Queued uploads are lost if it closes.</p>{/if}
        {/if}
    </div>

    {#if entry.kind === 'upload'}
        <ResumableUploadRow job={entry.job} />
        <dl>
            {#if jobDrive}<div><dt>Drive</dt><dd>{jobDrive}</dd></div>{/if}
            <div><dt>Uploaded</dt><dd>{formatSizePair(entry.job.confirmedBytes, entry.job.size)}</dd></div>
        </dl>
        <ActivityMessage title={entry.name} message={entry.job.error} />
    {:else if entry.kind === 'download'}
        <ResumableDownloadRow job={entry.job} />
        <dl>
            {#if jobDrive}<div><dt>Drive</dt><dd>{jobDrive}</dd></div>{/if}
            <div><dt>Downloaded</dt><dd>{formatSizePair(entry.job.verifiedBytes, entry.job.totalBytes)}</dd></div>
            {#if entry.job.savedPath}<div><dt>Saved to</dt><dd>{entry.job.savedPath}</dd></div>{/if}
        </dl>
        <ActivityMessage title={entry.name} message={entry.job.error} />
    {:else}
        {#if showTransfer && transfer}
            <TransferRow {transfer} onCancel={cancelTransfersInDirection} onCancelFile={cancelUploadFile} />
        {/if}
        {#if batch}
            <section class="files-section" aria-label="Files in upload">
                <h4>Files <span>{batch.fileCount.toLocaleString()}</span></h4>
                {#if batch.files.length > 0}
                    <p class="file-summary">{fileSummary}</p>
                    <ul class="file-list">
                        {#each pageRows as row (row.kind === 'folder' ? `folder:${row.folder}` : row.file.index)}
                            {#if row.kind === 'folder'}
                                <li class="file-folder">
                                    <button type="button" aria-expanded={row.open} onclick={() => toggleFolder(row.folder)}>
                                        <span class="folder-chevron" class:open={row.open} aria-hidden="true"><ChevronRightIcon size={14} /></span>
                                        <FolderIcon size={15} aria-hidden="true" />
                                        <span class="folder-path">{row.folder.split('/').join(' / ')}</span>
                                        <span class="folder-counts">{folderCounts(row)}{#if row.failed}<span class="failed">{` · ${row.failed} failed`}</span>{/if}</span>
                                    </button>
                                </li>
                            {:else}
                                {@const file = row.file}
                                {@const percent = activeFiles.get(String(file.index))?.progress ?? file.progress}
                                <li data-activity-file class:nested={row.nested}>
                                    <span class="file-name">{file.name}</span>
                                    <span class="file-status" class:failed={file.status === 'failed'}>{FILE_STATUS[file.status]}{file.status === 'active' && percent ? ` · ${Math.round(percent)}%` : ''}</span>
                                    {#if file.size}<span class="file-size">{formatBytes(file.size)}</span>{/if}
                                    {#if file.message}<p class="file-message">{file.message}</p>{/if}
                                </li>
                            {/if}
                        {/each}
                    </ul>
                    {#if pageCount > 1}
                        <div class="file-pagination" aria-label="File pages">
                            <button type="button" disabled={currentPage === 0} onclick={() => filePage = currentPage - 1}>Previous files</button>
                            <span>Page {currentPage + 1} of {pageCount}</span>
                            <button type="button" disabled={currentPage >= pageCount - 1} onclick={() => filePage = currentPage + 1}>Next files</button>
                        </div>
                    {/if}
                {:else}
                    <p class="detail-note">{batch.status === 'active' || batch.status === 'queued' ? 'Each file appears here once it starts uploading.' : `${fileCountLabel(batch.fileCount)} in this upload.`}</p>
                {/if}
            </section>
            <dl>
                <div><dt>Destination</dt><dd>{batch.driveTitle}{#if destinationPath}<span class="destination-path">{destinationPath}</span>{/if}</dd></div>
                <div><dt>Encryption</dt><dd>{batch.encrypt ? 'Encrypted' : 'Not encrypted'}</dd></div>
            </dl>
            {#if batch.status === 'active' && !transfer}
                <button type="button" class="detail-action danger" onclick={() => cancelTransfersInDirection('up')}>Cancel upload</button>
            {/if}
            <ActivityMessage title={entry.name} message={batch.error || ''} />
            {#if linkedJobs.length > 0}
                <section class="files-section" aria-label="Upload recovery"><h4>Upload recovery</h4><p class="detail-note">Resume or discard the saved upload. The file list shows the first attempt.</p>{#each linkedJobs as job (job.jobId)}<ResumableUploadRow {job} />{/each}</section>
            {/if}
        {:else if transfer}
            <ActivityMessage title={entry.name} message={transfer.note || ''} />
            {#if retry}<button type="button" class="detail-action" onclick={retry}>Retry download</button>{/if}
        {/if}
        <section class="timeline" aria-label="Timeline">
            <h4>Timeline</h4>
            <dl>
                {#if batch}<div><dt>Added</dt><dd>{activityTimestamp(batch.addedAt)}</dd></div>{/if}
                {#if startedAt}<div><dt>Started</dt><dd>{activityTimestamp(startedAt)}</dd></div>{/if}
                {#if finishedAt}<div><dt>Finished</dt><dd>{activityTimestamp(finishedAt)}</dd></div>{/if}
                {#if elapsed !== null}<div><dt>{finishedAt ? 'Took' : 'Elapsed'}</dt><dd>{duration(elapsed)}</dd></div>{/if}
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
    .detail-status[data-tone='warning'] { color: var(--color-warning); }
    .detail-status[data-tone='danger'], .failed { color: var(--color-danger); }
    .detail-actions { display: flex; flex-wrap: wrap; gap: var(--space-2); margin-top: var(--space-4); }
    dl { margin: var(--space-5) 0; display: grid; gap: var(--space-3); }
    dl > div { display: grid; grid-template-columns: 120px minmax(0, 1fr); gap: var(--space-3); font-size: var(--type-sm); }
    dt { color: var(--color-text-muted); }
    dd { margin: 0; overflow-wrap: anywhere; }
    .destination-path { display: block; margin-top: var(--space-1); color: var(--color-text-muted); }
    .detail-note, .file-summary { margin: var(--space-3) 0 0; color: var(--color-text-muted); font-size: var(--type-xs); line-height: var(--leading-normal); }
    h4 { margin: 0 0 var(--space-2); font-size: var(--type-base); font-weight: var(--weight-semibold); }
    h4 span { margin-left: var(--space-1); color: var(--color-text-muted); font-weight: var(--weight-regular); }
    .files-section, .timeline { margin-top: var(--space-6); padding-top: var(--space-5); border-top: 1px solid var(--color-border-soft); }
    /* Right under the progress card, the breakdown needs no divider above it. */
    :global(.notif-row) + .files-section, .detail-heading + .files-section { margin-top: var(--space-5); padding-top: 0; border-top: 0; }
    .file-summary { margin: 0; }
    .timeline dl { margin-bottom: 0; }
    .file-list { margin: var(--space-3) 0; padding: 0; list-style: none; }
    .file-list li { display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: var(--space-1) var(--space-3); padding: var(--space-3) 0; border-bottom: 1px solid var(--color-border-soft); }
    .file-list li:last-child { border-bottom: 0; }
    .file-list li.nested { padding-left: calc(var(--space-6) + var(--space-1)); }
    .file-list li.file-folder { display: block; padding: 0; }
    .file-folder button { display: flex; align-items: center; gap: var(--space-2); width: 100%; min-height: var(--control-lg); padding: var(--space-2) 0; border: 0; background: transparent; color: var(--color-text-soft); font: inherit; font-size: var(--type-sm); font-weight: var(--weight-semibold); text-align: left; cursor: pointer; }
    .file-folder :global(svg) { flex-shrink: 0; color: var(--color-text-muted); }
    .folder-chevron { display: grid; place-items: center; transition: transform var(--motion-fast) var(--ease-standard); }
    .folder-chevron.open { transform: rotate(90deg); }
    .folder-path { min-width: 0; flex: 1; overflow-wrap: anywhere; }
    .folder-counts { flex-shrink: 0; color: var(--color-text-muted); font-size: var(--type-xs); font-weight: var(--weight-regular); }
    @media (hover: hover) { .file-folder button:hover .folder-path { color: var(--color-text); } }
    @media (prefers-reduced-motion: reduce) { .folder-chevron { transition: none; } }
    .file-name { font-size: var(--type-sm); overflow-wrap: anywhere; }
    .file-status, .file-size { font-size: var(--type-xs); color: var(--color-text-muted); }
    .file-message { margin: var(--space-1) 0 0; grid-column: 1 / -1; color: var(--color-danger); font-size: var(--type-xs); overflow-wrap: anywhere; user-select: text; }
    .detail-action, .file-pagination button { min-height: var(--control-md); border: 1px solid var(--color-border-soft); border-radius: var(--radius-sm); padding: var(--space-1-5) var(--space-3); background: var(--color-surface-2); color: var(--color-text-soft); font: inherit; font-size: var(--type-xs); font-weight: var(--weight-semibold); cursor: pointer; }
    .detail-action.primary { border-color: var(--overlay-accent-2); background: var(--overlay-accent-1); color: var(--color-accent-text); }
    .detail-action.danger { color: var(--color-danger); }
    button:focus-visible { outline: 2px solid var(--color-accent); outline-offset: 2px; }
    button:disabled { opacity: .45; cursor: default; }
    @media (hover: hover) { .detail-action:hover, .file-pagination button:not(:disabled):hover { background: var(--overlay-neutral-1); } .detail-action.primary:hover { background: var(--overlay-accent-2); } }
    .file-pagination { display: flex; align-items: center; justify-content: space-between; gap: var(--space-2); font-size: var(--type-xs); color: var(--color-text-muted); }
    @media (max-width: 700px) { .activity-details { padding: var(--space-2) var(--space-4) var(--space-6); } dl > div { grid-template-columns: 100px minmax(0, 1fr); } button { min-height: var(--touch-target); } .activity-details :global(.actions button) { min-height: var(--touch-target); } .activity-details :global(.notif-row-cancel) { min-width: var(--touch-target); min-height: var(--touch-target); } .file-pagination { flex-wrap: wrap; } }
</style>
