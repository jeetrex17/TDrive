<script lang="ts">
    import ArrowDownIcon from '@lucide/svelte/icons/arrow-down';
    import ArrowUpIcon from '@lucide/svelte/icons/arrow-up';
    import RotateCwIcon from '@lucide/svelte/icons/rotate-cw';
    import Share2Icon from '@lucide/svelte/icons/share-2';
    import XIcon from '@lucide/svelte/icons/x';
    import { isMobilePlatform } from '../../api';
    import { cancelSingleUpload } from '../../modules/notif-bell';
    import { formatBytes } from '../../utils';
    import { isUnfinishedTransfer, type TransferEvent } from './notif-store';

    interface Props {
        transfer: TransferEvent;
        // Cancels everything moving in this row's direction. Used for a row the
        // backend can only stop wholesale: a download, or an import's single
        // aggregate row standing in for a batch.
        onCancel?: (direction: TransferEvent['direction']) => void;
        // Mobile Transfers tab only: reopens the share sheet for a finished
        // single-file download. Absent on desktop, so no action renders there.
        onShare?: () => void;
        // Mobile Transfers tab only: re-queues a failed download. Absent on
        // desktop, and never supplied for an upload, which has no source path
        // left to retry from.
        onRetry?: () => void;
        // Set when this row's × stops more than this row -- a download while
        // others are queued behind it. An upload cancels on its own, so it
        // leaves this unset and the button just says Cancel.
        cancelLabel?: string;
    }

    let { transfer, onCancel, onShare, onRetry, cancelLabel }: Props = $props();

    // The phone's Transfers tab is its own surface; the same row on desktop
    // lives in the bell popover and must keep rendering exactly as it did.
    const mobile = isMobilePlatform();

    const canShare = $derived(Boolean(onShare) && transfer.status === 'done' && transfer.direction === 'down');
    const canRetry = $derived(Boolean(onRetry) && transfer.status === 'failed');

    const direction = $derived(transfer.direction === 'up' ? 'upload' : 'download');
    const dirLabel = $derived(transfer.direction === 'up' ? 'Uploading' : 'Downloading');
    const statusClass = $derived(
        transfer.status === 'done' ? 'is-done'
        : transfer.status === 'failed' ? 'is-failed'
        : transfer.status === 'canceled' ? 'is-canceled'
        : 'is-active',
    );
    const progressWidth = $derived(Math.max(0, Math.min(100, transfer.progress || 0)));
    const progressLabel = $derived(`${dirLabel} ${transfer.name || 'transfer'}`);
    const terminalLabel = $derived(
        transfer.status === 'done' ? 'Done'
        : transfer.status === 'failed' ? 'Failed'
        : transfer.status === 'canceled' ? 'Canceled'
        : '',
    );
    const doneBytes = $derived(transfer.total > 0
        ? Math.min(transfer.total, Math.max(transfer.bytes || 0, ((transfer.progress || 0) / 100) * transfer.total))
        : transfer.bytes || 0);

    /**
     * On a phone, "3.7 / 42.4 MB" rather than "3.7 MB / 42.4 MB". Naming the
     * unit twice when it is the same unit both times is the sort of thing that
     * reads fine in a spreadsheet and crowds a phone, and the pair is one
     * quantity to the person reading it, not two. Desktop has the width and
     * keeps the spelled-out pair it has always had.
     */
    const sizePair = $derived.by(() => {
        const done = formatBytes(doneBytes);
        const total = formatBytes(transfer.total);
        if (!mobile) return `${done} / ${total}`;
        const doneUnit = done.slice(done.lastIndexOf(' ') + 1);
        if (doneUnit && doneUnit === total.slice(total.lastIndexOf(' ') + 1)) {
            return `${done.slice(0, done.lastIndexOf(' '))} / ${total}`;
        }
        return `${done} / ${total}`;
    });

    /**
     * A transfer that has started but cannot yet say how big it is: a folder
     * being walked before the first byte moves. There is no progress to report,
     * so it reports none rather than a confident 0%, and the bar says the work
     * is running by moving. Phone only: the indeterminate bar is a mobile
     * treatment, and without it the desktop row would just go blank.
     */
    const preparing = $derived(
        mobile
        && transfer.status === 'active'
        && transfer.total <= 0
        && (transfer.itemsTotal || 0) <= 0
        && (transfer.progress || 0) <= 0,
    );

    // The phone stacks these figures into one line and needs each one to be its
    // own box; the desktop column reads them as the plain text it always did.
    const metaClass = $derived(mobile ? 'notif-row-size' : '');

    /**
     * A single file of an upload batch can be stopped on its own, and the row
     * already carries the id the backend knows it by. Anything else -- a
     * download, an import's aggregate row -- has no such id and falls back to
     * stopping everything in its direction.
     */
    const uploadId = $derived.by(() => {
        const match = /^xfer:up:(\d+)$/.exec(transfer.id);
        return match ? Number(match[1]) : null;
    });

    function cancel(event: MouseEvent): void {
        event.stopPropagation();
        if (uploadId !== null) {
            cancelSingleUpload(uploadId);
            return;
        }
        onCancel?.(transfer.direction);
    }
</script>

<div class={`notif-row notif-row-transfer ${statusClass}${preparing ? ' is-preparing' : ''}`}>
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
        <div
            class="notif-row-progress"
            role="progressbar"
            aria-label={progressLabel}
            aria-valuemin="0"
            aria-valuemax="100"
            aria-valuenow={Math.round(progressWidth)}
        >
            <div
                class="notif-row-progress-fill"
                style={preparing ? undefined : `width:${progressWidth}%`}
                aria-hidden="true"
            ></div>
        </div>
    </div>
    {#if !preparing}
        <div class="notif-row-meta">
            {#if terminalLabel}
                <div class={metaClass}>{terminalLabel}</div>
            {:else if transfer.total <= 0}
                {#if (transfer.itemsTotal || 0) > 0}
                    <div class="notif-row-size">{transfer.itemsDone || 0} {mobile ? 'of' : '/'} {transfer.itemsTotal} files</div>
                {:else}
                    <div class={metaClass}>{Math.round(transfer.progress || 0)}%</div>
                {/if}
            {:else}
                {#if (transfer.itemsTotal || 0) > 0}
                    <div class="notif-row-size">{transfer.itemsDone || 0} {mobile ? 'of' : '/'} {transfer.itemsTotal} files</div>
                {/if}
                <div class="notif-row-size">{sizePair}</div>
                {#if transfer.speed > 0}
                    <div class="notif-row-speed">{formatBytes(transfer.speed)}/s</div>
                {/if}
            {/if}
        </div>
    {/if}
    {#if isUnfinishedTransfer(transfer.status)}
        <button
            class="notif-row-cancel"
            type="button"
            data-cancel-dir={transfer.direction}
            aria-label={cancelLabel || 'Cancel transfer'}
            title={cancelLabel || 'Cancel'}
            onclick={cancel}
        >
            <XIcon size={12} strokeWidth={2} aria-hidden="true" />
        </button>
    {:else if canRetry}
        <button
            class="notif-row-retry"
            type="button"
            aria-label={`Retry ${transfer.name || 'transfer'}`}
            title="Retry"
            onclick={(event) => { event.stopPropagation(); onRetry?.(); }}
        >
            <RotateCwIcon size={13} strokeWidth={2} aria-hidden="true" />
        </button>
    {:else if canShare}
        <button
            class="notif-row-share"
            type="button"
            aria-label={`Share ${transfer.name || 'file'}`}
            title="Share"
            onclick={(event) => { event.stopPropagation(); onShare?.(); }}
        >
            <Share2Icon size={13} strokeWidth={2} aria-hidden="true" />
        </button>
    {/if}
</div>

<style>
    /* Phone only; the desktop bell keeps the tones it has always had.

       The direction icon is accent because accent means "this is happening".
       On a row that failed, the same accent arrow says the transfer is alive
       while the label beside it says it is not, and the badge on the tab is
       pointing here precisely because it is not. Failure takes the danger
       tone, and the other finished states go quiet: on a done download the
       one thing still worth a colour is the Share button. */
    :global(html.mobile .notif-row-transfer.is-failed .notif-row-icon) {
        background: var(--overlay-danger-1);
        color: var(--danger);
    }
    :global(html.mobile .notif-row-transfer.is-done .notif-row-icon),
    :global(html.mobile .notif-row-transfer.is-canceled .notif-row-icon) {
        background: var(--overlay-neutral-2);
        color: var(--text-muted);
    }

    /* A failed download is the one terminal row with somewhere to go. Same
       shape as Share, in the danger tone the row now carries, so the row reads
       as "this broke, here is the way back". The box stays 32px so the row
       keeps its height; the ::after carries the thumb's 44px. */
    :global(html.mobile .notif-row-retry) {
        position: relative;
        flex: 0 0 auto;
        display: inline-grid;
        place-items: center;
        width: 32px;
        height: 32px;
        margin-left: 4px;
        border: 0;
        border-radius: var(--radius-sm);
        background: var(--overlay-danger-1);
        color: var(--danger);
        cursor: pointer;
    }
    :global(html.mobile .notif-row-retry::after) {
        content: '';
        position: absolute;
        inset: -6px;
    }
    :global(html.mobile .notif-row-retry:active) { background: var(--overlay-danger-2); }
</style>
