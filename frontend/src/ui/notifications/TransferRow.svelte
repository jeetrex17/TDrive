<script lang="ts">
    import ArrowDownIcon from '@lucide/svelte/icons/arrow-down';
    import ArrowUpIcon from '@lucide/svelte/icons/arrow-up';
    import Share2Icon from '@lucide/svelte/icons/share-2';
    import XIcon from '@lucide/svelte/icons/x';
    import { formatBytes } from '../../utils';
    import type { TransferEvent } from './notif-store';

    interface Props {
        transfer: TransferEvent;
        onCancel?: (direction: TransferEvent['direction']) => void;
        // Mobile Transfers tab only: reopens the share sheet for a finished
        // single-file download. Absent on desktop, so no action renders there.
        onShare?: () => void;
    }

    let { transfer, onCancel, onShare }: Props = $props();

    const canShare = $derived(Boolean(onShare) && transfer.status === 'done' && transfer.direction === 'down');

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
     * "3.7 / 42.4 MB" rather than "3.7 MB / 42.4 MB". Naming the unit twice
     * when it is the same unit both times is the sort of thing that reads fine
     * in a spreadsheet and crowds a phone, and the pair is one quantity to the
     * person reading it, not two.
     */
    const sizePair = $derived.by(() => {
        const done = formatBytes(doneBytes);
        const total = formatBytes(transfer.total);
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
     * is running by moving.
     */
    const preparing = $derived(
        transfer.status === 'active'
        && transfer.total <= 0
        && (transfer.itemsTotal || 0) <= 0
        && (transfer.progress || 0) <= 0,
    );

    function cancel(event: MouseEvent): void {
        event.stopPropagation();
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
                <div class="notif-row-size">{terminalLabel}</div>
            {:else if transfer.total <= 0}
                {#if (transfer.itemsTotal || 0) > 0}
                    <div class="notif-row-size">{transfer.itemsDone || 0} of {transfer.itemsTotal} files</div>
                {:else}
                    <div class="notif-row-size">{Math.round(transfer.progress || 0)}%</div>
                {/if}
            {:else}
                {#if (transfer.itemsTotal || 0) > 0}
                    <div class="notif-row-size">{transfer.itemsDone || 0} of {transfer.itemsTotal} files</div>
                {/if}
                <div class="notif-row-size">{sizePair}</div>
                {#if transfer.speed > 0}
                    <div class="notif-row-speed">{formatBytes(transfer.speed)}/s</div>
                {/if}
            {/if}
        </div>
    {/if}
    {#if transfer.status === 'active'}
        <button
            class="notif-row-cancel"
            type="button"
            data-cancel-dir={transfer.direction}
            aria-label="Cancel transfer"
            title="Cancel"
            onclick={cancel}
        >
            <XIcon size={12} strokeWidth={2} aria-hidden="true" />
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
