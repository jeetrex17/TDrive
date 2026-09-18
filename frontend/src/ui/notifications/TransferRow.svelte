<script lang="ts">
    import ArrowDownIcon from '@lucide/svelte/icons/arrow-down';
    import ArrowUpIcon from '@lucide/svelte/icons/arrow-up';
    import XIcon from '@lucide/svelte/icons/x';
    import { formatBytes } from '../../utils';
    import TransferItems from './TransferItems.svelte';
    import { etaSeconds, formatEta, formatSizePair } from './transfer-view';
    import { isUnfinishedTransfer, type TransferEvent } from './notif-store';

    /**
     * One transfer in the desktop bell's popover, where each row stands alone on
     * glass and carries its own border and corner.
     *
     * The phone has its own row (ui/mobile/MobileTransferRow). This one used to
     * serve both, gated on the platform in eight places and undone again by most
     * of a screenful of stylesheet overrides; the two surfaces want different
     * shapes and now each states its own.
     */
    interface Props {
        transfer: TransferEvent;
        /**
         * Cancels everything moving in this row's direction. The bell's rows are
         * its only cancel -- there is no Cancel all beside its section titles --
         * and only three uploads run at once, so a narrower control would leave
         * a twenty-file batch with no way to stop.
         */
        onCancel?: (direction: TransferEvent['direction']) => void;
        /**
         * Stops one file of an aggregate row. The row-wide control above takes
         * the whole batch down; this is how a reader drops the one 4 GB video
         * holding up the other hundred and ninety-nine files.
         */
        onCancelFile?: (key: string) => void;
    }

    let { transfer, onCancel, onCancelFile }: Props = $props();

    const direction = $derived(transfer.direction === 'up' ? 'upload' : 'download');
    const dirLabel = $derived(transfer.direction === 'up' ? 'Uploading' : 'Downloading');
    const statusClass = $derived(
        transfer.status === 'done' ? 'is-done'
        : transfer.status === 'failed' ? 'is-failed'
        : transfer.status === 'canceled' ? 'is-canceled'
        : transfer.status === 'canceling' ? 'is-canceling'
        : 'is-active',
    );
    const progressWidth = $derived(Math.max(0, Math.min(100, transfer.progress || 0)));
    const progressLabel = $derived(`${dirLabel} ${transfer.name || 'transfer'}`);
    const terminalLabel = $derived(
        transfer.status === 'done' ? 'Done'
        : transfer.status === 'failed' ? 'Failed'
        : transfer.status === 'canceled' ? 'Canceled'
        : transfer.status === 'canceling' ? 'Canceling'
        : '',
    );
    const doneBytes = $derived(transfer.total > 0
        ? Math.min(transfer.total, Math.max(transfer.bytes || 0, ((transfer.progress || 0) / 100) * transfer.total))
        : transfer.bytes || 0);

    const showProgress = $derived(isUnfinishedTransfer(transfer.status) && transfer.status !== 'canceling');
    const items = $derived(showProgress ? transfer.items ?? [] : []);
    const eta = $derived(etaSeconds(transfer));
    const canCancel = $derived(
        isUnfinishedTransfer(transfer.status)
        && transfer.status !== 'canceling'
        // The download backend can stop only the job it is currently running.
        // A queued row gets no fake individual cancel control.
        && transfer.id !== 'xfer:up:photo-backup'
        && (transfer.direction === 'up' || transfer.status === 'active'),
    );
    const cancelLabel = $derived(
        transfer.direction === 'down' ? 'Cancel active download' : 'Cancel all uploads',
    );

    function cancel(event: MouseEvent): void {
        event.stopPropagation();
        onCancel?.(transfer.direction);
    }
</script>

<div class={`notif-row notif-row-transfer ${statusClass}`}>
    <span class="notif-row-icon" data-kind={direction} aria-hidden="true">
        {#if transfer.direction === 'up'}
            <ArrowUpIcon size={14} strokeWidth={2} aria-hidden="true" />
        {:else}
            <ArrowDownIcon size={14} strokeWidth={2} aria-hidden="true" />
        {/if}
    </span>
    <div class="notif-row-body">
        <div class="notif-row-title" title={transfer.name}>{transfer.name || dirLabel}</div>
        <!-- A progressbar reports state when queried or focused, without a
             live region that would announce each transfer tick. -->
        {#if showProgress}
            <div
                class="notif-row-progress"
                role="progressbar"
                aria-label={progressLabel}
                aria-valuemin="0"
                aria-valuemax="100"
                aria-valuenow={Math.round(progressWidth)}
            >
                <div class="notif-row-progress-fill" style={`width:${progressWidth}%`} aria-hidden="true"></div>
            </div>
        {/if}
    </div>
    <!-- Read down: how many files, how far, how much, how long, how fast. The
         count leads because on an aggregate it is the figure the reader is
         actually tracking; the rate trails because it says the least. -->
    <div class="notif-row-meta">
        {#if terminalLabel}
            <div>{terminalLabel}</div>
        {:else}
            {#if (transfer.itemsTotal || 0) > 0}
                <div class="notif-row-size">{transfer.itemsDone || 0} / {transfer.itemsTotal} files</div>
            {:else}
                <!-- A row counting files has the bar to say how far along it is
                     and no room to spare; one with nothing to count needs the
                     figure, because it is the only one it has. -->
                <div class="notif-row-size">{Math.round(transfer.progress || 0)}%</div>
            {/if}
            {#if transfer.total > 0}
                <div class="notif-row-size">{formatSizePair(doneBytes, transfer.total)}</div>
            {:else if transfer.bytes > 0}
                <div class="notif-row-size">{formatBytes(transfer.bytes)} so far</div>
            {/if}
            {#if eta !== null}
                <div class="notif-row-size">{formatEta(eta)}</div>
            {/if}
            {#if transfer.speed > 0}
                <div class="notif-row-speed">{formatBytes(transfer.speed)}/s</div>
            {/if}
        {/if}
    </div>
    {#if canCancel}
        <button
            class="notif-row-cancel"
            type="button"
            data-cancel-dir={transfer.direction}
            aria-label={cancelLabel}
            title={cancelLabel}
            onclick={cancel}
        >
            <XIcon size={12} strokeWidth={2} aria-hidden="true" />
        </button>
    {/if}
    {#if items.length > 0}
        <div class="notif-row-items">
            <TransferItems {items} active={transfer.itemsActive ?? items.length} onCancel={onCancelFile} />
        </div>
    {/if}
</div>
