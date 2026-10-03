<script lang="ts">
    import ArrowUpIcon from '@lucide/svelte/icons/arrow-up';
    import type { ResumableUpload } from '../../api/resumable-uploads';
    import {
        checkUploadStatus, chooseSourceAndResume, discardUpload, pauseUpload, resumeUpload,
    } from '../../modules/resumable-uploads';
    import { formatSizePair } from '../notifications/transfer-view';

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
        : job.status === 'waiting_network' ? 'Waiting for connection'
        : job.status === 'needs_source' ? 'Original file needed'
        : job.status === 'uncertain' ? 'Part needs checking'
        : job.status === 'uncertain_manifest' ? 'Publication needs checking'
        : job.status === 'restart_required' ? 'Restart needed'
        : job.status === 'canceling' ? 'Discarding…'
        : 'Paused',
    );
    const guidance = $derived(
        job.status === 'needs_source'
            ? 'Choose the original file to verify it. Your phone may have removed its temporary copy.'
            : job.status === 'waiting_network'
                ? 'TDrive will retry when your phone is connected. You can also retry now.'
            : job.status === 'uncertain_manifest'
                ? 'TDrive cannot confirm whether this file was published. Its parts are being kept.'
                : job.status === 'uncertain'
                    ? 'The last part needs checking before this upload can continue.'
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

<div class="row resumable-row" data-phase={job.status} role="listitem">
    <span class="glyph" aria-hidden="true"><ArrowUpIcon size={15} strokeWidth={2.2} /></span>
    <div class="name" title={job.name}>{job.name || 'Large upload'}</div>
    <div
        class="track"
        role="progressbar"
        aria-label={`${job.name || 'Large upload'}, confirmed in Telegram`}
        aria-valuemin="0"
        aria-valuemax="100"
        aria-valuenow={Math.round(percent)}
    >
        <div class="fill" style={`width:${percent}%`} aria-hidden="true"></div>
    </div>
    <div class="detail">{stateLabel} · {formatSizePair(job.confirmedBytes, job.size)} confirmed</div>
    {#if guidance}<p class="note">{guidance}</p>{/if}
    {#if job.status !== 'canceling'}
        <div class="actions">
            {#if job.status === 'paused' || job.status === 'uncertain' || job.status === 'waiting_network'}
                <button type="button" disabled={busy} aria-label={`Resume ${job.name}`} onclick={() => resumeUpload(job.jobId)}>{job.status === 'waiting_network' ? 'Retry now' : 'Resume'}</button>
            {:else if job.status === 'needs_source'}
                <button type="button" disabled={busy} aria-label={`Choose original file for ${job.name}`} onclick={() => chooseSourceAndResume(job.jobId)}>Choose file</button>
            {:else if job.status === 'uploading'}
                <button type="button" disabled={busy} aria-label={`Pause ${job.name}`} onclick={() => void act(() => pauseUpload(job.jobId))}>Pause</button>
            {:else if job.status === 'uncertain_manifest'}
                <button type="button" disabled={busy} aria-label={`Check and retry ${job.name}`} onclick={() => void act(() => checkUploadStatus(job.jobId))}>{busy ? 'Checking…' : 'Check and retry'}</button>
            {/if}
            {#if job.status !== 'uploading' && job.status !== 'uncertain_manifest'}
                {#if confirmingDiscard}
                    <button type="button" disabled={busy} aria-label={`Keep ${job.name}`} onclick={() => confirmingDiscard = false}>Keep</button>
                    <button type="button" class="danger" disabled={busy} aria-label={`Discard parts for ${job.name}`} onclick={() => void act(() => discardUpload(job.jobId))}>Discard parts</button>
                {:else}
                    <button type="button" class="danger" disabled={busy} aria-label={`Discard ${job.name}`} onclick={() => confirmingDiscard = true}>Discard</button>
                {/if}
            {/if}
        </div>
    {/if}
</div>

<style>
    .row {
        display: grid;
        grid-template-columns: 28px minmax(0, 1fr);
        align-items: start;
        column-gap: 12px;
        row-gap: 7px;
        padding: 12px 14px;
    }
    .glyph {
        display: grid;
        place-items: center;
        width: 28px;
        height: 28px;
        margin-top: 1px;
        border-radius: var(--radius-pill);
        background: var(--overlay-accent-1);
        color: var(--color-accent);
    }
    .row[data-phase='restart_required'] .glyph,
    .row[data-phase='uncertain_manifest'] .glyph {
        background: var(--overlay-danger-1);
        color: var(--color-danger);
    }
    .name {
        min-width: 0;
        overflow: hidden;
        text-overflow: ellipsis;
        white-space: nowrap;
        font-size: var(--mobile-type-body);
        font-weight: var(--weight-medium);
        color: var(--color-text);
    }
    .track, .detail, .note, .actions { grid-column: 2 / -1; }
    .track {
        height: 5px;
        border-radius: var(--radius-pill);
        background: var(--overlay-neutral-2);
        overflow: hidden;
    }
    .fill {
        height: 100%;
        border-radius: inherit;
        background: var(--color-accent);
        transition: width var(--motion-med) var(--ease-standard);
    }
    .detail, .note {
        margin: 0;
        font-size: var(--mobile-type-meta);
        color: var(--color-text-muted);
    }
    .detail { font-variant-numeric: tabular-nums; }
    .note { font-size: var(--mobile-type-caption); line-height: 1.45; }
    .actions { display: flex; flex-wrap: wrap; gap: 4px 8px; margin-top: 1px; }
    .actions button {
        min-height: 44px;
        padding: 0 12px;
        border: 0;
        border-radius: var(--radius-md);
        background: var(--overlay-accent-1);
        color: var(--color-accent);
        font: inherit;
        font-size: var(--mobile-type-meta);
        font-weight: var(--weight-medium);
        cursor: pointer;
    }
    .actions button.danger { background: var(--overlay-danger-1); color: var(--color-danger); }
    .actions button:active { filter: brightness(.94); }
    .actions button:focus-visible { outline: 2px solid var(--color-accent); outline-offset: 2px; }
    .actions button:disabled { opacity: .55; cursor: wait; }
    @media (prefers-reduced-motion: reduce) { .fill { transition: none; } }
</style>
