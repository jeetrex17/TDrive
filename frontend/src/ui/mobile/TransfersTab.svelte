<script lang="ts">
    import ArrowDownUpIcon from '@lucide/svelte/icons/arrow-down-up';
    import { activeTransfers, type TransferEvent } from '../notifications/notif-store';
    import EventRow from '../notifications/EventRow.svelte';
    import MobileTransferRow from './MobileTransferRow.svelte';
    import { cancelSingleUpload, cancelTransfersInDirection, clearHistory } from '../../modules/notif-bell';
    import { humanizeBackendError } from '../../modules/errors';
    import { notify } from '../../modules/notifications';
    import { downloadRetryFor } from '../../modules/transfers';
    import { shareFile } from '../../api';
    import { downloadSharePaths, forgetDownloadSharePath, recentTransferEvents } from './mobile-shell-store';

    /** How many transfers are in flight; more than one earns a way to stop the lot. */
    const inFlight = $derived($activeTransfers.length);

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
        if (transfer.id === 'xfer:up:photo-backup') return undefined;
        const upload = /^xfer:up:(\d+)$/.exec(transfer.id);
        if (upload) {
            const id = Number(upload[1]);
            return () => cancelSingleUpload(id);
        }
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
        cancelTransfersInDirection('up');
        cancelTransfersInDirection('down');
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
    {#if inFlight === 0 && $recentTransferEvents.length === 0}
        <div class="mobile-empty">
            <span class="mobile-empty-glyph">
                <ArrowDownUpIcon size={40} strokeWidth={1.6} aria-hidden="true" />
            </span>
            <p class="mobile-empty-title">No transfers</p>
            <p class="mobile-empty-body">Uploads and downloads show up here.</p>
        </div>
    {:else}
        {#if inFlight > 0}
            <div class="transfers-section-head">
                <h2 class="mobile-section-label">Active</h2>
                {#if inFlight > 1}
                    <button type="button" class="transfers-clear" onclick={cancelEverything}>Cancel all</button>
                {/if}
            </div>
            <div class="transfers-group" role="list">
                {#each $activeTransfers as transfer (transfer.id)}
                    <MobileTransferRow {transfer} onCancel={cancelFor(transfer)} />
                {/each}
            </div>
        {/if}
        {#if $recentTransferEvents.length > 0}
            <div class="transfers-section-head">
                <h2 class="mobile-section-label">Recent</h2>
                <button type="button" class="transfers-clear" onclick={clearHistory}>Clear</button>
            </div>
            <div class="transfers-group" role="list">
                {#each $recentTransferEvents.slice(0, 50) as entry (entry.id)}
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
</div>

<style>
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
