<script lang="ts">
    import ArrowUpIcon from '@lucide/svelte/icons/arrow-up';
    import type { ResumableUpload } from '../../api/resumable-uploads';
    import { checkUploadStatus, chooseSourceAndResume, discardUpload, pauseUpload, resumeUpload } from '../../modules/resumable-uploads';
    import { formatSizePair } from './transfer-view';

    interface Props { job: ResumableUpload }
    let { job }: Props = $props();
    let busy = $state(false);
    let confirmingDiscard = $state(false);
    let confirmationStatus = $state<string | null>(null);
    $effect(() => {
        if (job.status === confirmationStatus) return;
        confirmationStatus = job.status;
        confirmingDiscard = false;
    });

    const percent = $derived(job.size > 0 ? Math.min(100, job.confirmedBytes / job.size * 100) : 0);
    const stateLabel = $derived(
        job.status === 'uploading' ? 'Uploading'
        : job.status === 'paused' ? 'Paused'
        : job.status === 'needs_source' ? 'Source file needed'
        : job.status === 'uncertain' ? 'Checking needed'
        : job.status === 'uncertain_manifest' ? 'Needs review'
        : job.status === 'restart_required' ? 'Restart needed'
        : job.status === 'canceling' ? 'Discarding…'
        : 'Paused',
    );
    const detail = $derived(`${stateLabel} · ${formatSizePair(job.confirmedBytes, job.size)} confirmed`);
    const guidance = $derived(
        job.status === 'needs_source'
            ? 'The source moved or changed. Choose the original file to verify it.'
            : job.status === 'uncertain_manifest'
                ? 'TDrive cannot yet confirm whether this file was published. Leave its parts in place.'
                : job.status === 'uncertain'
                    ? 'The last part needs checking before the upload can continue.'
                : job.status === 'restart_required'
                    ? 'The file changed during upload. Discard these parts, then start a new upload.'
                : '',
    );

    async function act(action: () => Promise<void>): Promise<void> {
        if (busy) return;
        busy = true;
        try { await action(); }
        finally { busy = false; }
    }
</script>

<div class="notif-row notif-row-transfer resumable-row" data-phase={job.status}>
    <span class="notif-row-icon" data-kind="upload" aria-hidden="true">
        <ArrowUpIcon size={14} strokeWidth={2} aria-hidden="true" />
    </span>
    <div class="notif-row-body">
        <div class="notif-row-title" title={job.name}>{job.name || 'Large upload'}</div>
        <div
            class="notif-row-progress"
            role="progressbar"
            aria-label={`${job.name || 'Large upload'}, confirmed in Telegram`}
            aria-valuemin="0"
            aria-valuemax="100"
            aria-valuenow={Math.round(percent)}
        >
            <div class="notif-row-progress-fill" style={`width:${percent}%`} aria-hidden="true"></div>
        </div>
        {#if guidance}<div class="notif-row-note">{guidance}</div>{/if}
    </div>
    <div class="notif-row-meta"><div class="notif-row-state">{detail}</div></div>
    {#if job.status !== 'canceling'}
        <div class="actions">
            {#if job.status === 'paused' || job.status === 'uncertain'}
                <button class="notif-row-copy" type="button" disabled={busy} onclick={() => resumeUpload(job.jobId)}>Resume</button>
            {:else if job.status === 'needs_source'}
                <button class="notif-row-copy" type="button" disabled={busy} onclick={() => chooseSourceAndResume(job.jobId)}>Choose file</button>
            {:else if job.status === 'uploading'}
                <button class="notif-row-copy" type="button" disabled={busy} onclick={() => void act(() => pauseUpload(job.jobId))}>Pause</button>
            {:else if job.status === 'uncertain_manifest'}
                <button class="notif-row-copy" type="button" disabled={busy} onclick={() => void act(() => checkUploadStatus(job.jobId))}>{busy ? 'Checking…' : 'Check and retry'}</button>
            {/if}
            {#if job.status !== 'uploading' && job.status !== 'uncertain_manifest'}
                {#if confirmingDiscard}
                    <button class="notif-row-copy" type="button" disabled={busy} onclick={() => confirmingDiscard = false}>Keep</button>
                    <button class="notif-row-copy danger" type="button" disabled={busy} onclick={() => void act(() => discardUpload(job.jobId))}>Discard parts</button>
                {:else}
                    <button class="notif-row-copy danger" type="button" disabled={busy} onclick={() => confirmingDiscard = true}>Discard</button>
                {/if}
            {/if}
        </div>
    {/if}
</div>

<style>
    .resumable-row { grid-template-columns: 22px minmax(0, 1fr) auto; row-gap: 8px; }
    .resumable-row .notif-row-meta { grid-column: 2 / -1; margin-left: 0; align-items: flex-start; }
    .resumable-row .notif-row-body { grid-column: 2 / -1; }
    .resumable-row .notif-row-progress { margin-top: 2px; }
    .resumable-row .notif-row-state { color: var(--color-text-soft); font-variant-numeric: tabular-nums; }
    .actions { grid-column: 2 / -1; display: flex; flex-wrap: wrap; gap: 6px; }
    .actions .notif-row-copy { min-height: 28px; }
    .actions .danger { color: var(--color-danger); }
    .actions .danger:hover { background: var(--overlay-danger-1); }
    .actions .notif-row-copy:focus-visible { outline: 2px solid var(--color-accent); outline-offset: 2px; }
    .actions .notif-row-copy:disabled { opacity: .55; cursor: wait; }
</style>
