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
    import { downloadSharePaths } from './mobile-shell-store';

    /**
     * What the row's x actually does. An upload stops on its own now, so it
     * just says Cancel. Downloads still share one cancel handle, so with more
     * than one of them here the button takes the rest too -- worth saying on
     * the button rather than finding out afterwards.
     */
    function cancelLabelFor(transfer: TransferEvent): string | undefined {
        if (transfer.direction === 'up') return undefined;
        const together = $activeTransfers.filter((entry) => entry.direction === 'down').length;
        return together > 1 ? `Cancel all ${together} downloads` : undefined;
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
                            onRetry={downloadRetryFor(entry)}
                        />
                    {:else}
                        <EventRow event={entry} />
                    {/if}
                {/each}
            </div>
        {/if}
    {/if}
</div>
