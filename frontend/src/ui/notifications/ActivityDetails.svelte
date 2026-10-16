<script lang="ts">
    import { readable } from 'svelte/store';
    import { resumableUploads } from '../../modules/resumable-upload-store';
    import { removeQueuedUpload } from '../../modules/upload-queue';
    import { cancelTransfersInDirection, cancelUploadFile } from '../../modules/notif-bell';
    import { cancelActivityFile, downloadRetryFor, reviewFailedUploads, setUploadsPaused } from '../../modules/transfers';
    import { switchActiveChannel } from '../../modules/channels';
    import { formatBytes } from '../../utils';
    import { failedUploadReports } from '../modals/failed-uploads-store';
    import { sidebarState } from '../sidebar/sidebar-store';
    import ChevronRightIcon from '@lucide/svelte/icons/chevron-right';
    import XIcon from '@lucide/svelte/icons/x';
    import FolderIcon from '@lucide/svelte/icons/folder';
    import PauseIcon from '@lucide/svelte/icons/pause';
    import PlayIcon from '@lucide/svelte/icons/play';
    import CheckIcon from '@lucide/svelte/icons/check';
    import { fileTypeFamily, fileTypeIcon } from '../file-list/file-type';
    import { uploadsPaused, type UploadActivityFile } from './upload-activity-store';
    import type { ActivityEntry } from './activity-model';
    import { activityTimestamp, batchLabel, fileCountLabel, groupFilesByFolder, isBatchPaused } from './activity-model';
    import { etaSeconds, formatEta, formatSizePair, transferPercent, transferredBytes } from './transfer-view';
    import TransferRow from './TransferRow.svelte';
    import ResumableUploadRow from './ResumableUploadRow.svelte';
    import ResumableDownloadRow from './ResumableDownloadRow.svelte';
    import ActivityMessage from './ActivityMessage.svelte';

    interface Props { entry: ActivityEntry; activeDriveId?: number | null; queuedAhead?: number; uploadRunning?: boolean }
    let { entry, activeDriveId, queuedAhead = 0, uploadRunning = false }: Props = $props();
    let filePage = $state(0);
    let collapsed = $state<string[]>([]);
    type FileFilter = 'all' | 'active' | 'queued' | 'failed' | 'done' | 'canceled';
    let fileFilter = $state<FileFilter>('all');
    const PAGE_SIZE = 50;
    const FILE_STATUS = { done: 'Completed', active: 'Uploading', queued: 'Waiting', failed: 'Failed', canceled: 'Canceled' } as const;
    const batch = $derived(entry.kind === 'batch' ? entry.batch : null);
    const transfer = $derived(entry.kind === 'transfer' || entry.kind === 'batch' ? entry.transfer : undefined);
    const activeFiles = $derived(new Map((transfer?.items ?? []).map((item) => [item.key, item])));
    // Loose files as they are, then one heading per folder with its files under
    // it. A folded folder keeps its heading and counts.
    type FileRow =
        | { kind: 'folder'; folder: string; total: number; done: number; failed: number; active: number; saving: number; open: boolean }
        | { kind: 'file'; file: UploadActivityFile; nested: boolean };
    const rows = $derived.by(() => {
        const out: FileRow[] = [];
        const shown = (batch?.files ?? []).filter((file) => fileFilter === 'all' || file.status === fileFilter);
        for (const { folder, files } of groupFilesByFolder(shown)) {
            const open = !collapsed.includes(folder);
            if (folder) {
                const count = (status: UploadActivityFile['status']) => files.filter((file) => file.status === status).length;
                const saving = files.filter(isSaving).length;
                out.push({ kind: 'folder', folder, total: files.length, done: count('done'), failed: count('failed'), active: count('active') - saving, saving, open });
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
    // Telegram has these; only their local index entries are still being written.
    const isSaving = (file: UploadActivityFile) => file.status === 'active' && (file.progress ?? 0) >= 100;
    // One chip per state the batch actually has, so the list can be narrowed to
    // what is moving, waiting or went wrong.
    const CHIPS: ReadonlyArray<[FileFilter, string]> = [['all', 'All'], ['active', 'Uploading'], ['queued', 'Waiting'], ['failed', 'Failed'], ['done', 'Done'], ['canceled', 'Canceled']];
    // An import names its files as it reaches them; the rest are still to come.
    const unlisted = $derived(batch && (batch.status === 'active' || batch.status === 'queued') ? Math.max(0, batch.fileCount - batch.files.length) : 0);
    // "All" carries no count: the header already says how many files there are,
    // and a count of only the files listed so far would contradict it.
    // With a single state there is nothing to narrow, so the chips stay hidden.
    const chips = $derived.by(() => {
        const shown = CHIPS
            .map(([filter, label]) => ({ filter, label, count: filter === 'all' ? 0 : (batch?.files.filter((file) => file.status === filter).length ?? 0) + (filter === 'queued' ? unlisted : 0) }))
            .filter((chip) => chip.filter === 'all' || chip.count > 0);
        return shown.length > 2 || fileFilter !== 'all' ? shown : [];
    });
    const folderName = $derived.by(() => {
        if (!batch) return '';
        const path = batch.destination.startsWith(`${batch.driveTitle} / `) ? batch.destination.slice(batch.driveTitle.length + 3) : '';
        return path ? path.split(' / ').pop() || batch.driveTitle : batch.driveTitle;
    });
    const headline = $derived(!batch ? entry.name
        : batch.status === 'queued' ? `Waiting to upload to ${folderName}`
        : batch.status === 'active' ? ($uploadsPaused ? `Upload to ${folderName} paused` : `Uploading to ${folderName}`)
        : batch.status === 'done' ? `Uploaded to ${folderName}`
        : batch.status === 'failed' ? `Upload to ${folderName} needs attention`
        : `Upload to ${folderName} canceled`);
    // The full path only when the title's folder does not already say it.
    const fullPath = $derived(batch ? `${batch.driveTitle}${destinationPath ? ` / ${destinationPath}` : ''}` : '');
    const meta = $derived(batch ? [
        fileCountLabel(batch.fileCount),
        fullPath === folderName ? '' : fullPath,
        batch.encrypt ? 'Encrypted' : 'Not encrypted',
    ].filter(Boolean).join(' · ') : '');
    const percent = $derived(transfer ? transferPercent(transfer) : null);
    const figures = $derived.by(() => {
        if (!batch || !transfer) return { left: '', right: '' };
        const done = batch.files.filter((file) => file.status === 'done').length;
        const bytes = transfer.total > 0 ? formatSizePair(transferredBytes(transfer), transfer.total) : transfer.bytes > 0 ? `${formatBytes(transfer.bytes)} so far` : '';
        const eta = etaSeconds(transfer, $clock);
        return {
            left: [`${done.toLocaleString()} of ${fileCountLabel(batch.fileCount)}`, bytes].filter(Boolean).join(' · '),
            right: $uploadsPaused ? '' : [eta !== null ? formatEta(eta) : '', transfer.speed > 0 ? `${formatBytes(transfer.speed)}/s` : ''].filter(Boolean).join(' · '),
        };
    });
    const startedAt = $derived(batch?.startedAt || transfer?.startedAt || 0);
    const finishedAt = $derived(batch?.finishedAt || transfer?.finishedAt || 0);
    const elapsed = $derived(startedAt > 0 && (finishedAt || batch?.status === 'active' || transfer?.status === 'active')
        ? Math.max(0, Math.round(((finishedAt || $clock) - startedAt) / 1000)) : null);
    const running = $derived(batch?.status === 'active');
    const status = $derived.by(() => {
        if (!batch) return entry.summary;
        if (running) return $uploadsPaused ? 'Files already sending will finish. The rest wait until you resume.' : '';
        // The title already says done or canceled; a failure says how much.
        if (batch.status === 'done' || batch.status === 'canceled') return '';
        if (batch.status === 'failed' && !linkedJobs.length) {
            const failed = batch.files.filter((file) => file.status === 'failed').length;
            return failed ? `${failed} of ${fileCountLabel(batch.fileCount)} didn't upload` : batchLabel(batch, $resumableUploads, activeDriveId);
        }
        if (batch.status !== 'queued' || paused) return batchLabel(batch, $resumableUploads, activeDriveId);
        if (queuedAhead > 0) return `Queued · ${queuedAhead} ${queuedAhead === 1 ? 'upload' : 'uploads'} ahead`;
        return uploadRunning ? 'Queued · Starts after the current upload' : 'Queued · Starting soon';
    });
    const retry = $derived(entry.kind === 'transfer' ? downloadRetryFor(entry.transfer) : undefined);

    function filePercent(file: UploadActivityFile): number | undefined {
        return activeFiles.get(String(file.index))?.progress ?? file.progress;
    }
    function fileStatus(file: UploadActivityFile): string {
        if (file.status === 'active') {
            const progress = filePercent(file);
            // Telegram has it; only the local index entry is still being written.
            if (progress !== undefined && progress >= 100) return 'Saving…';
            return progress ? `${Math.round(progress)}%` : 'Uploading';
        }
        if (file.status === 'queued') return running && $uploadsPaused ? 'Paused' : 'Waiting';
        return FILE_STATUS[file.status];
    }
    function canCancel(file: UploadActivityFile): boolean {
        return running && (file.status === 'queued' || (file.status === 'active' && fileStatus(file) !== 'Saving…'));
    }
    function chooseFilter(filter: FileFilter): void {
        fileFilter = filter;
        filePage = 0;
    }

    function toggleFolder(folder: string): void {
        collapsed = collapsed.includes(folder) ? collapsed.filter((entry) => entry !== folder) : [...collapsed, folder];
    }
    function folderCounts(row: Extract<FileRow, { kind: 'folder' }>): string {
        return [fileCountLabel(row.total), row.done ? `${row.done} done` : '', row.saving ? `${row.saving} saving` : '', row.active ? `${row.active} uploading` : '']
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
    {#if batch}
        <header class="batch-header">
            <div class="batch-title-row">
                <div class="batch-title">
                    <h3>{headline}</h3>
                    <p class="batch-meta">{meta}</p>
                </div>
                {#if running || batch.status === 'queued' || report}
                    <div class="batch-actions">
                        {#if running}
                            <button type="button" class="pill" onclick={() => void setUploadsPaused(!$uploadsPaused)}>
                                {#if $uploadsPaused}<PlayIcon size={14} aria-hidden="true" />{:else}<PauseIcon size={14} aria-hidden="true" />{/if}{$uploadsPaused ? 'Resume' : 'Pause'}
                            </button>
                            <button type="button" class="pill quiet" onclick={() => cancelTransfersInDirection('up')}><XIcon size={14} aria-hidden="true" />Cancel upload</button>
                        {/if}
                        {#if paused}<button type="button" class="pill accent" onclick={() => void switchActiveChannel(batch.driveId)}>Open {batch.driveTitle}</button>{/if}
                        {#if report}<button type="button" class="pill accent" onclick={() => reviewFailedUploads(report)}>Review failed files</button>{/if}
                        {#if batch.status === 'queued'}<button type="button" class="pill quiet" onclick={() => removeQueuedUpload(batch.id)}><XIcon size={14} aria-hidden="true" />Remove from queue</button>{/if}
                    </div>
                {/if}
            </div>
            {#if running && transfer}
                <div class="batch-progress" class:paused={$uploadsPaused} class:indeterminate={percent === null} role="progressbar" aria-label="Upload progress"
                    aria-valuemin="0" aria-valuemax="100" aria-valuenow={percent === null ? undefined : Math.round(percent)}>
                    <span style={percent === null ? undefined : `width:${percent}%`}></span>
                </div>
                <div class="batch-figures"><span>{figures.left}</span><span>{figures.right}</span></div>
            {/if}
            {#if status}<p class="detail-status" data-tone={paused ? 'warning' : entry.status === 'Needs attention' ? 'danger' : undefined}>{status}</p>{/if}
            {#if batch.status === 'queued'}<p class="detail-note">Keep TDrive open. Queued uploads are lost if it closes.</p>{/if}
        </header>

        <ActivityMessage title={entry.name} message={batch.error || ''} />
        {#if linkedJobs.length > 0}
            <section class="files-section" aria-label="Upload recovery"><h4>Upload recovery</h4><p class="detail-note">Resume or discard the saved upload. The file list shows the first attempt.</p>{#each linkedJobs as job (job.jobId)}<ResumableUploadRow {job} />{/each}</section>
        {/if}

        <section class="files-section" aria-label="Files in upload">
            {#if batch.files.length > 0}
                {#if chips.length}
                    <div class="file-chips" role="group" aria-label="Show files">
                        {#each chips as chip (chip.filter)}
                            <button type="button" aria-pressed={fileFilter === chip.filter} onclick={() => chooseFilter(chip.filter)}>{chip.label}{#if chip.filter !== 'all'}<span>{chip.count.toLocaleString()}</span>{/if}</button>
                        {/each}
                    </div>
                {/if}
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
                            {@const TypeIcon = fileTypeIcon(fileTypeFamily(file.name.includes('.') ? file.name.split('.').pop() ?? '' : ''))}
                            {@const progress = filePercent(file)}
                            <li data-activity-file data-status={file.status} class:nested={row.nested}>
                                <span class="file-tile" aria-hidden="true"><TypeIcon size={15} strokeWidth={1.75} /></span>
                                <span class="file-main">
                                    <span class="file-name">{file.name}</span>
                                    {#if file.size}<span class="file-size">{formatBytes(file.size)}</span>{/if}
                                    {#if file.status === 'active' && progress !== undefined && progress < 100}
                                        <span class="file-bar" aria-hidden="true"><span style={`width:${progress}%`}></span></span>
                                    {/if}
                                </span>
                                <span class="file-status" class:failed={file.status === 'failed'}>
                                    {#if file.status === 'done'}<CheckIcon size={15} strokeWidth={2.25} aria-hidden="true" /><span class="sr-only">Completed</span>{:else}{fileStatus(file)}{/if}
                                </span>
                                {#if canCancel(file)}
                                    <button type="button" class="file-cancel" aria-label={`Cancel ${file.name}`} onclick={() => cancelActivityFile(batch.id, file)}><XIcon size={14} aria-hidden="true" /></button>
                                {/if}
                                {#if file.message && file.status === 'failed'}<p class="file-message">{file.message}</p>{/if}
                            </li>
                        {/if}
                    {/each}
                </ul>
                {#if unlisted > 0 && (fileFilter === 'all' || fileFilter === 'queued')}<p class="detail-note">{unlisted.toLocaleString()} more {unlisted === 1 ? 'file' : 'files'} will appear here as the upload reaches them.</p>{/if}
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
    {:else}
        <div class="detail-heading">
            <span class="detail-eyebrow">{entry.kind === 'download' || (transfer?.direction === 'down') ? 'Download' : 'Upload'}</span>
            <h3>{entry.name}</h3>
            <p class="detail-status" data-tone={entry.status === 'Needs attention' ? 'danger' : undefined}>{status}</p>
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
        {:else if transfer}
            {#if showTransfer}<TransferRow {transfer} onCancel={cancelTransfersInDirection} onCancelFile={cancelUploadFile} />{/if}
            <ActivityMessage title={entry.name} message={transfer.note || ''} />
            {#if retry}<button type="button" class="pill" onclick={retry}>Retry download</button>{/if}
        {/if}
    {/if}
    {#if entry.kind === 'batch' || entry.kind === 'transfer'}
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
    /* Tonal layers and hairlines rather than cards inside cards: the header
       carries the batch, the list carries the files, and nothing is boxed twice. */
    .activity-details { min-width: 0; padding: var(--space-6); font-variant-numeric: tabular-nums; }
    .batch-header { display: grid; gap: var(--space-3); padding-bottom: var(--space-5); border-bottom: 1px solid var(--color-border-soft); }
    .batch-title-row { display: flex; align-items: flex-start; justify-content: space-between; gap: var(--space-4); }
    .batch-title { min-width: 0; }
    h3 { margin: 0; font-size: var(--type-lg); font-weight: var(--weight-semibold); letter-spacing: -0.01em; overflow-wrap: anywhere; text-wrap: balance; }
    .batch-meta { margin: var(--space-1) 0 0; color: var(--color-text-muted); font-size: var(--type-xs); overflow-wrap: anywhere; }
    .batch-actions { display: flex; flex-wrap: wrap; justify-content: flex-end; gap: var(--space-2); flex-shrink: 0; }
    .batch-progress { position: relative; height: 4px; border-radius: var(--radius-pill); background: var(--overlay-neutral-2); overflow: hidden; }
    .batch-progress span { display: block; height: 100%; border-radius: inherit; background: var(--color-accent); transition: width var(--motion-med) var(--ease-standard); }
    .batch-progress.paused span { background: var(--color-text-muted); }
    .batch-progress.indeterminate span { width: 30%; animation: batch-slide 1.4s var(--ease-standard) infinite; }
    @keyframes batch-slide { from { transform: translateX(-100%); } to { transform: translateX(340%); } }
    .batch-figures { display: flex; justify-content: space-between; gap: var(--space-3); color: var(--color-text-muted); font-size: var(--type-xs); }
    .detail-heading { margin-bottom: var(--space-5); }
    .detail-eyebrow { color: var(--color-text-muted); font-size: var(--type-xs); }
    .detail-heading h3 { margin: var(--space-1) 0 var(--space-2); font-size: var(--type-xl); }
    .detail-status { margin: 0; color: var(--color-text-muted); font-size: var(--type-sm); }
    .detail-status[data-tone='warning'] { color: var(--color-warning); }
    .detail-status[data-tone='danger'], .failed { color: var(--color-danger); }
    .detail-note { margin: var(--space-3) 0 0; color: var(--color-text-muted); font-size: var(--type-xs); line-height: var(--leading-normal); }
    .batch-header .detail-note { margin: 0; }

    /* Pills for every action, as the filters are: one shape for "something you can press". */
    .pill { display: inline-flex; align-items: center; gap: var(--space-1-5); height: 30px; padding: 0 var(--space-3); border: 1px solid var(--color-border-soft); border-radius: var(--radius-pill); background: var(--color-surface-2); color: var(--color-text-soft); font: inherit; font-size: var(--type-xs); font-weight: var(--weight-semibold); white-space: nowrap; cursor: pointer; transition: background-color var(--motion-fast) var(--ease-standard), color var(--motion-fast) var(--ease-standard); }
    .pill.quiet { background: transparent; color: var(--color-text-muted); }
    .pill.accent { border-color: var(--overlay-accent-2); background: var(--overlay-accent-1); color: var(--color-accent-text); }
    @media (hover: hover) {
        .pill:hover { background: var(--overlay-neutral-1); color: var(--color-text); }
        .pill.quiet:hover { color: var(--color-danger); }
        .pill.accent:hover { background: var(--overlay-accent-2); color: var(--color-accent-text); }
    }

    .files-section, .timeline { margin-top: var(--space-5); }
    .timeline { padding-top: var(--space-5); border-top: 1px solid var(--color-border-soft); }
    h4 { margin: 0 0 var(--space-2); font-size: var(--type-sm); font-weight: var(--weight-semibold); }
    dl { margin: var(--space-3) 0; display: grid; gap: var(--space-2); }
    dl > div { display: grid; grid-template-columns: 120px minmax(0, 1fr); gap: var(--space-3); font-size: var(--type-xs); }
    dt { color: var(--color-text-muted); }
    dd { margin: 0; overflow-wrap: anywhere; }
    .timeline dl { margin-bottom: 0; }

    .file-chips { display: flex; flex-wrap: wrap; gap: var(--space-1); margin-bottom: var(--space-2); }
    .file-chips button { display: inline-flex; align-items: center; gap: var(--space-1-5); height: 26px; padding: 0 var(--space-2-5); border: 1px solid transparent; border-radius: var(--radius-pill); background: transparent; color: var(--color-text-muted); font: inherit; font-size: var(--type-xs); cursor: pointer; }
    .file-chips button span { color: var(--color-text-subtle); }
    .file-chips button[aria-pressed='true'] { border-color: var(--color-border-soft); background: var(--color-surface-2); color: var(--color-text); }
    @media (hover: hover) { .file-chips button[aria-pressed='false']:hover { color: var(--color-text-soft); } }

    .file-list { margin: 0; padding: 0; list-style: none; }
    .file-list li { display: grid; grid-template-columns: 28px minmax(0, 1fr) auto 28px; column-gap: var(--space-3); align-items: center; min-height: 44px; padding: var(--space-1-5) 0; border-bottom: 1px solid var(--color-border-soft); }
    .file-list li:last-child { border-bottom: 0; }
    .file-list li.nested { padding-left: var(--space-6); }
    .file-tile { display: grid; place-items: center; width: 28px; height: 28px; border-radius: var(--radius-sm); background: var(--overlay-white-1); color: var(--color-text-soft); }
    .file-main { display: grid; min-width: 0; gap: 2px; }
    .file-name { overflow: hidden; color: var(--color-text); font-size: var(--type-sm); text-overflow: ellipsis; white-space: nowrap; }
    .file-size { color: var(--color-text-muted); font-size: var(--type-xs); }
    .file-bar { display: block; height: 2px; margin-top: 3px; border-radius: var(--radius-pill); background: var(--overlay-neutral-2); overflow: hidden; }
    .file-bar span { display: block; height: 100%; background: var(--color-accent); transition: width var(--motion-med) var(--ease-standard); }
    .file-status { display: inline-flex; align-items: center; color: var(--color-text-muted); font-size: var(--type-xs); white-space: nowrap; }
    li[data-status='done'] .file-status { color: var(--color-success); }
    .file-status.failed { color: var(--color-danger); }
    /* Waiting and stopped files step back so what is moving reads first. */
    li[data-status='queued'] .file-name, li[data-status='canceled'] .file-name { color: var(--color-text-muted); }
    li[data-status='queued'] .file-tile, li[data-status='canceled'] .file-tile { opacity: .55; }
    li[data-status='failed'] .file-tile { background: var(--overlay-danger-1); color: var(--color-danger); }
    .file-cancel { grid-column: 4; display: grid; place-items: center; width: 28px; height: 28px; padding: 0; border: 0; border-radius: var(--radius-pill); background: transparent; color: var(--color-text-muted); cursor: pointer; }
    @media (hover: hover) { .file-cancel:hover { background: var(--overlay-danger-1); color: var(--color-danger); } }
    .file-message { grid-column: 2 / -1; margin: var(--space-1) 0 0; color: var(--color-danger); font-size: var(--type-xs); overflow-wrap: anywhere; user-select: text; }
    .file-list li.file-folder { display: block; min-height: 0; padding: 0; }
    .file-folder button { display: flex; align-items: center; gap: var(--space-2); width: 100%; min-height: 40px; padding: var(--space-2) 0; border: 0; background: transparent; color: var(--color-text-soft); font: inherit; font-size: var(--type-xs); font-weight: var(--weight-semibold); text-align: left; cursor: pointer; }
    .file-folder :global(svg) { flex-shrink: 0; color: var(--color-text-muted); }
    .folder-chevron { display: grid; place-items: center; transition: transform var(--motion-fast) var(--ease-standard); }
    .folder-chevron.open { transform: rotate(90deg); }
    .folder-path { min-width: 0; flex: 1; overflow-wrap: anywhere; }
    .folder-counts { flex-shrink: 0; color: var(--color-text-muted); font-weight: var(--weight-regular); }
    @media (hover: hover) { .file-folder button:hover .folder-path { color: var(--color-text); } }

    .file-pagination { display: flex; align-items: center; justify-content: space-between; gap: var(--space-2); margin-top: var(--space-3); font-size: var(--type-xs); color: var(--color-text-muted); }
    .file-pagination button { height: 28px; padding: 0 var(--space-3); border: 1px solid var(--color-border-soft); border-radius: var(--radius-pill); background: transparent; color: var(--color-text-soft); font: inherit; font-size: var(--type-xs); cursor: pointer; }
    .sr-only { position: absolute; width: 1px; height: 1px; padding: 0; margin: -1px; overflow: hidden; clip: rect(0 0 0 0); white-space: nowrap; border: 0; }
    button:focus-visible { outline: 2px solid var(--color-accent); outline-offset: 2px; }
    button:disabled { opacity: .45; cursor: default; }
    @media (prefers-reduced-motion: reduce) {
        .folder-chevron, .pill, .batch-progress span, .file-bar span { transition: none; }
        .batch-progress.indeterminate span { animation: none; width: 100%; opacity: .5; }
    }
    @media (max-width: 700px) {
        .activity-details { padding: var(--space-2) var(--space-4) var(--space-6); }
        .batch-title-row { flex-direction: column; }
        .batch-actions { justify-content: flex-start; }
        .pill { height: var(--touch-target); padding: 0 var(--space-4); font-size: var(--type-sm); }
        .file-chips button { height: 36px; padding: 0 var(--space-3); font-size: var(--type-sm); }
        dl > div { grid-template-columns: 100px minmax(0, 1fr); }
        .file-list li { grid-template-columns: 32px minmax(0, 1fr) auto var(--touch-target); min-height: 52px; }
        .file-list li.nested { padding-left: var(--space-4); }
        .file-cancel { width: var(--touch-target); height: var(--touch-target); }
        .file-folder button { min-height: var(--touch-target); }
        .file-pagination { flex-wrap: wrap; }
        .file-pagination button { height: var(--touch-target); }
        .activity-details :global(.notif-row-cancel) { min-width: var(--touch-target); min-height: var(--touch-target); }
    }
</style>
