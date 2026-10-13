<script lang="ts">
    import ModalShell from './ModalShell.svelte';
    import { failedUploadsModal } from './failed-uploads-store';

    interface Props { onClose: () => void; onRetry: () => void; }
    let { onClose, onRetry }: Props = $props();
    const view = failedUploadsModal.state;
    const PAGE_SIZE = 20;
    let page = $state(0);
    let previousPayload: unknown = null;
    const failures = $derived($view.payload?.failures ?? []);
    const retryCount = $derived(failures.filter((failure) => failure.retryable).length);
    const pages = $derived(Math.max(1, Math.ceil(failures.length / PAGE_SIZE)));
    $effect(() => {
        if ($view.payload !== previousPayload) page = 0;
        previousPayload = $view.payload;
    });
    function guidance(code: string): string {
        if (code === 'projection_pending' || code === 'outcome_unknown') return 'Sync this drive to check whether the file arrived before uploading again.';
        if (code === 'resumable_upload') return 'Use this upload’s Resume controls in Transfers.';
        if (code === 'canceled') return 'This file was canceled.';
        if (code === 'retry_started') return 'A retry was already started. Review its result in Transfers.';
        return 'Resolve the reported problem before selecting this file again.';
    }
</script>

<ModalShell hostId="failed-uploads-modal" open={$view.open} title="Failed uploads" titleId="failed-uploads-title"
    subtitle={`${failures.length} ${failures.length === 1 ? 'file needs' : 'files need'} attention in ${$view.payload?.driveTitle ?? 'the original drive'}. Successful files will not be retried.`}
    initialFocus="#retry-failed-uploads" restoreFocus="#file-list" onClose={onClose}>
    <ul class="failed-upload-list" aria-label="Failed files">
        {#each failures.slice(page * PAGE_SIZE, (page + 1) * PAGE_SIZE) as failure (failure.index)}
            <li>
                <strong>{failure.name}</strong>
                <p>{failure.message}</p>
                <small>{failure.retryable ? 'Ready to retry' : guidance(failure.code)}</small>
            </li>
        {/each}
    </ul>
    {#if pages > 1}
        <nav class="failed-upload-pages" aria-label="Failed files pages">
            <button class="secondary-btn" type="button" disabled={page === 0} onclick={() => page -= 1}>Previous</button>
            <span>Page {page + 1} of {pages}</span>
            <button class="secondary-btn" type="button" disabled={page + 1 >= pages} onclick={() => page += 1}>Next</button>
        </nav>
    {/if}
    {#if $view.error}<p role="alert">{$view.error}</p>{/if}
    {#snippet actions()}
        <button class="secondary-btn" type="button" onclick={onClose}>Close</button>
        <button id="retry-failed-uploads" class="primary-btn" type="button" disabled={retryCount === 0 || $view.busy} onclick={onRetry}>
            Retry failed files{retryCount > 0 ? ` (${retryCount})` : ''}
        </button>
    {/snippet}
</ModalShell>

<style>
    .failed-upload-list { list-style: none; padding: 0; margin: 0; max-height: 45vh; overflow-y: auto; }
    li { padding: var(--space-3); border-bottom: 1px solid var(--border); overflow-wrap: anywhere; }
    p { margin: var(--space-1) 0; }
    small { color: var(--text-muted); }
    .failed-upload-pages { display: flex; align-items: center; justify-content: space-between; gap: var(--space-2); margin-top: var(--space-3); }
</style>
