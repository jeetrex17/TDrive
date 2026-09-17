<script lang="ts">
    import ArrowDownUpIcon from '@lucide/svelte/icons/arrow-down-up';
    import { activeTransfers, recentEvents, type TransferEvent } from '../notifications/notif-store';
    import EventRow from '../notifications/EventRow.svelte';
    import TransferRow from '../notifications/TransferRow.svelte';
    import { cancelTransfersInDirection, clearHistory } from '../../modules/notif-bell';
    import { humanizeBackendError } from '../../modules/errors';
    import { notify } from '../../modules/notifications';
    import { downloadRetryFor } from '../../modules/transfers';
    import { shareFile } from '../../api';
    import { downloadSharePaths, forgetDownloadSharePath } from './mobile-shell-store';

    /**
     * What the row's x actually does. An upload stops on its own now, so it
     * just says Cancel. A download row can stop only the current backend job,
     * so queued rows expose no cancellation control.
     */
    function cancelLabelFor(transfer: TransferEvent): string | undefined {
        return transfer.direction === 'up' ? 'Cancel all uploads' : 'Cancel active download';
    }

    // Finished single-file downloads keep their sandbox path so the share sheet
    // can be reopened from here (spec 2.5: Recent items get a Share action).
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

    /**
     * The row's x stops one file. A batch needs its own way out: with three
     * uploads running at a time, twenty files show three rows, and stopping
     * those three just lets the next three start. Shown only when there is
     * more than one upload, so a single transfer keeps one obvious control.
     */
    const activeUploads = $derived($activeTransfers.filter((t) => t.direction === 'up').length);

    function cancelAllUploads(): void {
        cancelTransfersInDirection('up');
    }

    function retryFor(transfer: TransferEvent): (() => void) | undefined {
        const retry = downloadRetryFor(transfer);
        if (!retry) return undefined;
        return () => {
            forgetDownloadSharePath(transfer.id);
            retry();
        };
    }
</script>

<div class="mobile-scroll transfers-tab">
    {#if $activeTransfers.length === 0 && $recentEvents.length === 0}
        <div class="mobile-empty">
            <span class="mobile-empty-glyph">
                <ArrowDownUpIcon size={40} strokeWidth={1.6} aria-hidden="true" />
            </span>
            <p class="mobile-empty-title">No transfers</p>
            <p class="mobile-empty-body">Uploads and downloads show up here.</p>
        </div>
    {:else}
        {#if $activeTransfers.length > 0}
            <div class="transfers-section-head">
                <h2 class="mobile-section-label">Active</h2>
                {#if activeUploads > 1}
                    <button type="button" class="transfers-clear" aria-label="Cancel all uploads" onclick={cancelAllUploads}>
                        Cancel all
                    </button>
                {/if}
            </div>
            <div class="transfers-group" role="list">
                {#each $activeTransfers as transfer (transfer.id)}
                    <TransferRow
                        {transfer}
                        onCancel={cancelTransfersInDirection}
                        cancelLabel={cancelLabelFor(transfer)}
                    />
                {/each}
            </div>
        {/if}
        {#if $recentEvents.length > 0}
            <div class="transfers-section-head">
                <h2 class="mobile-section-label">Recent</h2>
                <button type="button" class="transfers-clear" onclick={clearHistory}>Clear</button>
            </div>
            <div class="transfers-group" role="list">
                {#each $recentEvents.slice(0, 50) as entry (entry.id)}
                    {#if entry.kind === 'transfer'}
                        <TransferRow
                            transfer={entry}
                            onShare={shareFor(entry.id)}
                            onRetry={retryFor(entry)}
                        />
                    {:else}
                        <EventRow event={entry} listItem />
                    {/if}
                {/each}
            </div>
        {/if}
    {/if}
</div>

<style>
    /* Keep the quiet text treatment while giving the section actions an iOS
       and Android sized touch target. The pseudo-element extends the hit area
       without changing the heading rhythm. */
    :global(html.mobile .transfers-clear) { position: relative; }
    :global(html.mobile .transfers-clear::after) {
        content: '';
        position: absolute;
        inset: -8px;
    }
</style>
