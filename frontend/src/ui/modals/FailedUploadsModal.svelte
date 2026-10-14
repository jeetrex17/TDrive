<script lang="ts">
    import BanIcon from '@lucide/svelte/icons/ban';
    import CircleXIcon from '@lucide/svelte/icons/circle-x';
    import ClockIcon from '@lucide/svelte/icons/clock';
    import PauseIcon from '@lucide/svelte/icons/pause';
    import RotateCcwIcon from '@lucide/svelte/icons/rotate-ccw';
    import TriangleAlertIcon from '@lucide/svelte/icons/triangle-alert';
    import ModalShell from './ModalShell.svelte';
    import { failedUploadsModal } from './failed-uploads-store';
    import { humanizeBackendError } from '../../modules/errors';
    import type { UploadFailure } from '../../types';

    interface Props { onClose: () => void; onRetry: () => void; }
    let { onClose, onRetry }: Props = $props();
    const view = failedUploadsModal.state;
    const PAGE_SIZE = 20;
    let page = $state(0);
    let previousPayload: unknown = null;
    const failures = $derived($view.payload?.failures ?? []);
    const total = $derived($view.payload?.paths.length ?? 0);
    const uploaded = $derived(Math.max(0, total - failures.length));
    const retryCount = $derived(failures.filter((failure) => failure.retryable).length);
    const checkCount = $derived(failures.filter((failure) => statusFor(failure).tone === 'warning').length);
    const pages = $derived(Math.max(1, Math.ceil(failures.length / PAGE_SIZE)));
    const driveTitle = $derived($view.payload?.driveTitle ?? 'the original drive');
    const subtitle = $derived(uploaded > 0
        ? `${failures.length} of ${total} files didn't upload to ${driveTitle}. Files that uploaded won't be sent again.`
        : `${failures.length} ${failures.length === 1 ? "file didn't" : "files didn't"} upload to ${driveTitle}.`);
    $effect(() => {
        if ($view.payload !== previousPayload) page = 0;
        previousPayload = $view.payload;
    });

    type Tone = 'accent' | 'warning' | 'info' | 'muted' | 'danger';
    interface Status { tone: Tone; label: string; detail: string; icon: typeof CircleXIcon; }

    // The label says what the reader can do; the detail says why. Uncertain
    // sends replace the transport error, which would otherwise invite a resend.
    function statusFor(failure: UploadFailure): Status {
        // The label already offers the retry, so drop the reason's own "Try again".
        if (failure.retryable) return { tone: 'accent', label: 'Ready to retry', detail: humanizeBackendError(new Error(failure.message)).replace(/\s*Try again\.$/, ''), icon: RotateCcwIcon };
        switch (failure.code) {
            case 'projection_pending':
                return { tone: 'warning', label: 'Indexing', detail: 'Telegram accepted this file. It will appear after the next sync.', icon: ClockIcon };
            case 'outcome_unknown':
                return { tone: 'warning', label: 'Not confirmed', detail: 'Telegram may have accepted this file. Check the folder before uploading it again.', icon: TriangleAlertIcon };
            case 'resumable_upload':
                return { tone: 'info', label: 'Paused', detail: 'Use this upload’s Resume controls in Transfers.', icon: PauseIcon };
            case 'retry_started':
                return { tone: 'info', label: 'Retry started', detail: 'A retry was already started. Review its result in Transfers.', icon: ClockIcon };
            case 'canceled':
                return { tone: 'muted', label: 'Canceled', detail: 'This file was canceled.', icon: BanIcon };
            default:
                return { tone: 'danger', label: 'Failed', detail: humanizeBackendError(new Error(failure.message)), icon: CircleXIcon };
        }
    }
</script>

<ModalShell hostId="failed-uploads-modal" open={$view.open} title="Failed uploads" titleId="failed-uploads-title"
    {subtitle} initialFocus="#retry-failed-uploads" restoreFocus="#file-list" onClose={onClose}>
    <div class="failed-upload-body">
    {#if retryCount > 0 || checkCount > 0}
        <div class="failed-upload-counts">
            {#if retryCount > 0}<span class="failure-count" data-tone="accent">{retryCount} ready to retry</span>{/if}
            {#if checkCount > 0}<span class="failure-count" data-tone="warning">{checkCount} {checkCount === 1 ? 'needs' : 'need'} checking</span>{/if}
        </div>
    {/if}
    <ul class="failed-upload-list" aria-label="Failed files">
        {#each failures.slice(page * PAGE_SIZE, (page + 1) * PAGE_SIZE) as failure (failure.index)}
            {@const status = statusFor(failure)}
            <li>
                <span class="failure-icon" data-tone={status.tone} aria-hidden="true">
                    <status.icon size={14} strokeWidth={2} aria-hidden="true" />
                </span>
                <div class="failure-body">
                    <span class="failure-name" title={failure.name}>{failure.name}</span>
                    <span class="failure-detail"><span class="failure-label" data-tone={status.tone}>{status.label}</span> · {status.detail}</span>
                </div>
            </li>
        {/each}
    </ul>
    {#if pages > 1}
        <nav class="failed-upload-pages" aria-label="Failed files pages">
            <span>{page * PAGE_SIZE + 1}–{Math.min((page + 1) * PAGE_SIZE, failures.length)} of {failures.length}</span>
            <div class="page-buttons">
                <button type="button" disabled={page === 0} onclick={() => page -= 1}>Previous</button>
                <button type="button" disabled={page + 1 >= pages} onclick={() => page += 1}>Next</button>
            </div>
        </nav>
    {/if}
    {#if $view.error}<p class="failed-upload-error" role="alert">{$view.error}</p>{/if}
    </div>
    {#snippet actions()}
        <button class="secondary-btn" type="button" onclick={onClose}>Close</button>
        <button id="retry-failed-uploads" class="primary-btn" type="button" disabled={retryCount === 0 || $view.busy} onclick={onRetry}>
            Retry failed files{retryCount > 0 ? ` (${retryCount})` : ''}
        </button>
    {/snippet}
</ModalShell>

<style>
    /* Spaced apart from the dialog actions below. */
    .failed-upload-body { display: flex; flex-direction: column; gap: var(--space-3); margin-bottom: var(--space-4); }
    .failed-upload-counts { display: flex; flex-wrap: wrap; gap: var(--space-2); }
    .failure-count {
        padding: 3px 9px;
        border-radius: var(--radius-pill);
        font-size: 0.76rem;
        font-weight: var(--weight-semibold);
        font-variant-numeric: tabular-nums;
    }
    .failure-count[data-tone='accent'] { background: var(--overlay-accent-1); color: var(--accent); }
    .failure-count[data-tone='warning'] { background: var(--overlay-warning-1); color: var(--color-warning); }

    .failed-upload-list {
        list-style: none;
        padding: 0;
        margin: 0;
        max-height: 45vh;
        overflow-y: auto;
        border: 1px solid var(--glass-border);
        border-radius: var(--radius-lg);
        background: var(--overlay-white-1);
    }
    li {
        display: grid;
        grid-template-columns: 22px minmax(0, 1fr);
        gap: 12px;
        align-items: start;
        padding: 10px 12px;
    }
    li + li { border-top: 1px solid var(--color-border-soft); }
    .failure-icon {
        width: 22px;
        height: 22px;
        display: inline-flex;
        align-items: center;
        justify-content: center;
        border-radius: var(--radius-pill);
    }
    .failure-icon[data-tone='accent'] { background: var(--overlay-accent-1); color: var(--accent); }
    .failure-icon[data-tone='warning'] { background: var(--overlay-warning-1); color: var(--color-warning); }
    .failure-icon[data-tone='info'] { background: var(--overlay-accent-1); color: var(--accent); }
    .failure-icon[data-tone='muted'] { background: var(--overlay-white-2); color: var(--text-muted); }
    .failure-icon[data-tone='danger'] { background: var(--overlay-danger-1); color: var(--danger); }
    .failure-body { min-width: 0; display: flex; flex-direction: column; gap: 2px; }
    .failure-name {
        color: var(--text-main);
        font-weight: var(--weight-medium);
        white-space: nowrap;
        overflow: hidden;
        text-overflow: ellipsis;
    }
    .failure-detail { color: var(--text-muted); font-size: 0.8rem; line-height: 1.35; }
    .failure-label { font-weight: var(--weight-semibold); }
    .failure-label[data-tone='accent'], .failure-label[data-tone='info'] { color: var(--accent); }
    .failure-label[data-tone='warning'] { color: var(--color-warning); }
    .failure-label[data-tone='danger'] { color: var(--danger); }

    .failed-upload-pages {
        display: flex;
        align-items: center;
        justify-content: space-between;
        gap: var(--space-2);
        color: var(--text-muted);
        font-size: 0.8rem;
        font-variant-numeric: tabular-nums;
    }
    .page-buttons { display: flex; gap: 4px; }
    .page-buttons button {
        min-height: 28px;
        padding: 0 10px;
        border: 1px solid var(--color-border-soft);
        border-radius: var(--radius-sm);
        background: transparent;
        color: var(--text-main);
        font: inherit;
        font-size: 0.8rem;
        cursor: pointer;
    }
    @media (hover: hover) {
        .page-buttons button:hover:not(:disabled) { background: var(--overlay-white-2); }
    }
    .page-buttons button:disabled { opacity: 0.4; cursor: default; }
    :global(html.mobile) .page-buttons button { min-height: 36px; padding: 0 14px; }
    .failed-upload-error { margin: 0; color: var(--danger); font-size: 0.85rem; }
</style>
