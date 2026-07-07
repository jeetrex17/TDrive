<script lang="ts">
    import { formatBytes } from '../../utils';
    import type { TransferEvent } from './notif-store';

    interface Props {
        transfer: TransferEvent;
        onCancel?: (direction: TransferEvent['direction']) => void;
    }

    let { transfer, onCancel }: Props = $props();

    const direction = $derived(transfer.direction === 'up' ? 'upload' : 'download');
    const dirLabel = $derived(transfer.direction === 'up' ? 'Uploading' : 'Downloading');
    const statusClass = $derived(
        transfer.status === 'done' ? 'is-done'
        : transfer.status === 'failed' ? 'is-failed'
        : transfer.status === 'canceled' ? 'is-canceled'
        : 'is-active',
    );
    const progressWidth = $derived(Math.max(0, Math.min(100, transfer.progress || 0)));
    const terminalLabel = $derived(
        transfer.status === 'done' ? 'Done'
        : transfer.status === 'failed' ? 'Failed'
        : transfer.status === 'canceled' ? 'Canceled'
        : '',
    );
    const doneBytes = $derived(
        transfer.total > 0 ? Math.min(transfer.total, ((transfer.progress || 0) / 100) * transfer.total) : 0,
    );

    // pointerdown, not click: the panel re-renders on every progress tick,
    // which can replace the button between mousedown and mouseup and eat a
    // click. It also runs before the row's copy handler can see the event.
    function onCancelPointerDown(event: PointerEvent): void {
        event.preventDefault();
        event.stopPropagation();
        onCancel?.(transfer.direction);
    }

    // Keyboard activation: pointerdown never fires for Enter/Space, and the
    // native click those keys synthesize is not handled either.
    function onCancelKeydown(event: KeyboardEvent): void {
        if (event.key !== 'Enter' && event.key !== ' ') return;
        event.preventDefault();
        event.stopPropagation();
        onCancel?.(transfer.direction);
    }
</script>

<div class={`notif-row notif-row-transfer ${statusClass}`}>
    <span class="notif-row-icon" data-kind={direction} aria-hidden="true">
        {#if transfer.direction === 'up'}
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 19V5"/><path d="M5 12l7-7 7 7"/></svg>
        {:else}
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 5v14"/><path d="M5 12l7 7 7-7"/></svg>
        {/if}
    </span>
    <div class="notif-row-body">
        <div class="notif-row-title" title={transfer.name}>{transfer.name || dirLabel}</div>
        <div class="notif-row-progress">
            <div class="notif-row-progress-fill" style={`width:${progressWidth}%`}></div>
        </div>
    </div>
    <div class="notif-row-meta">
        {#if terminalLabel}
            {terminalLabel}
        {:else if transfer.total <= 0}
            {Math.round(transfer.progress || 0)}%
        {:else}
            <div class="notif-row-size">{formatBytes(doneBytes)} / {formatBytes(transfer.total)}</div>
            {#if transfer.speed > 0}
                <div class="notif-row-speed">{formatBytes(transfer.speed)}/s</div>
            {/if}
        {/if}
    </div>
    {#if transfer.status === 'active'}
        <button
            class="notif-row-cancel"
            type="button"
            data-cancel-dir={transfer.direction}
            aria-label="Cancel transfer"
            title="Cancel"
            onpointerdown={onCancelPointerDown}
            onkeydown={onCancelKeydown}
        >
            &times;
        </button>
    {/if}
</div>
