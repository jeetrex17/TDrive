<script lang="ts">
    import ArrowDownIcon from '@lucide/svelte/icons/arrow-down';
    import ArrowUpIcon from '@lucide/svelte/icons/arrow-up';
    import RotateCwIcon from '@lucide/svelte/icons/rotate-cw';
    import Share2Icon from '@lucide/svelte/icons/share-2';
    import XIcon from '@lucide/svelte/icons/x';
    import type { TransferEvent } from '../notifications/notif-store';
    import {
        isMoving,
        transferAriaLabel,
        transferDetail,
        transferPercent,
        transferPhase,
    } from '../notifications/transfer-view';

    /**
     * One transfer, on a phone.
     *
     * The desktop bell's row lives in a popover where each entry stands alone on
     * glass and earns its own border and corner. Stacked inside a card that
     * already has both, the same markup needed most of a screenful of overrides
     * to undo -- `display: contents` on its body, every child re-placed by
     * grid-area, negative margins to claw back a thumb-sized target. This is the
     * phone's own row instead, and it leaves that one to desktop.
     *
     * Every row has the same three-part shape whatever it is reporting: the name,
     * a bar while there is still somewhere to go, and one line saying where it
     * has got to. Only the bar comes and goes, so a queue of mixed states reads
     * down the page evenly instead of changing height row by row.
     */
    interface Props {
        transfer: TransferEvent;
        /** Stops this transfer. Absent where the backend cannot stop it alone. */
        onCancel?: () => void;
        /** Re-queues a failed download. Absent for uploads, which have no source left. */
        onRetry?: () => void;
        /** Reopens the share sheet for a finished download that kept its path. */
        onShare?: () => void;
    }

    let { transfer, onCancel, onRetry, onShare }: Props = $props();

    const phase = $derived(transferPhase(transfer));
    const percent = $derived(transferPercent(transfer));
    const detail = $derived(transferDetail(transfer));
    const label = $derived(transferAriaLabel(transfer));
    const showBar = $derived(isMoving(phase));

    // One control per row at most. Stopping outranks the rest because it is the
    // only one that acts on work still in flight; the other two are offers about
    // work that has already finished.
    const control = $derived(
        onCancel && isMoving(phase) ? 'cancel'
        : onRetry && phase === 'failed' ? 'retry'
        : onShare && phase === 'done' ? 'share'
        : 'none',
    );
</script>

<div class="row" data-phase={phase} role="listitem">
    <span class="glyph" aria-hidden="true">
        {#if transfer.direction === 'up'}
            <ArrowUpIcon size={15} strokeWidth={2.2} />
        {:else}
            <ArrowDownIcon size={15} strokeWidth={2.2} />
        {/if}
    </span>

    <div class="name" title={transfer.name}>{transfer.name || 'Transfer'}</div>

    {#if control === 'cancel'}
        <button class="control" type="button" aria-label={`Stop ${transfer.name || 'transfer'}`} onclick={onCancel}>
            <XIcon size={17} strokeWidth={2} aria-hidden="true" />
        </button>
    {:else if control === 'retry'}
        <button class="control is-retry" type="button" aria-label={`Retry ${transfer.name || 'transfer'}`} onclick={onRetry}>
            <RotateCwIcon size={16} strokeWidth={2} aria-hidden="true" />
        </button>
    {:else if control === 'share'}
        <button class="control is-share" type="button" aria-label={`Share ${transfer.name || 'file'}`} onclick={onShare}>
            <Share2Icon size={16} strokeWidth={2} aria-hidden="true" />
        </button>
    {/if}

    {#if showBar}
        <!-- Reports itself when queried or focused, without a live region that
             would announce every tick of a transfer nobody asked to hear. -->
        <div
            class="track"
            class:is-indeterminate={percent === null}
            role="progressbar"
            aria-label={label}
            aria-valuemin="0"
            aria-valuemax="100"
            aria-valuenow={percent ?? undefined}
        >
            <div class="fill" style={percent === null ? undefined : `width:${percent}%`} aria-hidden="true"></div>
        </div>
    {/if}

    <div class="detail">{detail}</div>

    {#if transfer.note}
        <!-- Where a phone download landed. Its own line because it is a place,
             not a figure, and because it is the answer the person came for. -->
        <div class="note">{transfer.note}</div>
    {/if}
</div>

<style>
    /* Three columns and three rows: the glyph holds the first cell, the name and
       the control share the first row, and the bar and the detail run the full
       width beneath them. The control gets a cell of its own rather than landing
       in the implicit fourth, which is where it used to end up -- orphaned on a
       second row underneath the glyph. */
    .row {
        display: grid;
        grid-template-columns: 28px minmax(0, 1fr) auto;
        align-items: start;
        column-gap: 12px;
        row-gap: 7px;
        padding: 12px 14px;
    }

    .glyph {
        grid-area: 1 / 1;
        display: grid;
        place-items: center;
        width: 28px;
        height: 28px;
        margin-top: 1px;
        border-radius: var(--radius-pill);
        background: var(--overlay-accent-1);
        color: var(--color-accent);
    }

    /* Accent means "this is happening", so it is spent only on the rows where
       something is. On a row that failed, an accent arrow says the transfer is
       alive while the line beneath it says it is not. */
    .row[data-phase='failed'] .glyph { background: var(--overlay-danger-1); color: var(--color-danger); }
    .row[data-phase='done'] .glyph,
    .row[data-phase='canceled'] .glyph,
    .row[data-phase='canceling'] .glyph {
        background: var(--overlay-neutral-2);
        color: var(--color-text-muted);
    }

    .name {
        grid-area: 1 / 2;
        min-width: 0;
        font-size: var(--mobile-type-body);
        font-weight: var(--weight-medium);
        color: var(--color-text);
        overflow: hidden;
        text-overflow: ellipsis;
        white-space: nowrap;
    }

    .track {
        grid-area: 2 / 2 / auto / -1;
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

    /* Nothing true to report yet, so the bar says the work is running rather
       than standing at a figure it does not have. */
    .is-indeterminate .fill {
        width: 38%;
        transition: none;
        animation: row-indeterminate 1.25s var(--ease-standard) infinite;
    }
    @keyframes row-indeterminate {
        from { transform: translateX(-105%); }
        to { transform: translateX(268%); }
    }

    .detail {
        grid-area: 3 / 2 / auto / -1;
        min-width: 0;
        font-size: var(--mobile-type-meta);
        color: var(--color-text-muted);
        font-variant-numeric: tabular-nums;
        overflow: hidden;
        text-overflow: ellipsis;
        white-space: nowrap;
    }

    .note {
        grid-area: 4 / 2 / auto / -1;
        min-width: 0;
        font-size: var(--mobile-type-caption);
        color: var(--color-text-subtle);
        overflow: hidden;
        text-overflow: ellipsis;
        white-space: nowrap;
    }

    /* 40px for the thumb, pulled back out of the row so a target this size does
       not set the height of the line the name sits on and leave the bar
       floating a long way underneath it. */
    .control {
        grid-area: 1 / 3;
        display: grid;
        place-items: center;
        width: 40px;
        height: 40px;
        margin: -7px -9px -5px 0;
        padding: 0;
        border: 0;
        border-radius: var(--radius-md);
        background: transparent;
        color: var(--color-text-muted);
        cursor: pointer;
    }
    .control:active { background: var(--color-surface-2); }
    .control:focus-visible {
        outline: 2px solid var(--color-accent);
        outline-offset: -2px;
    }

    /* A failed download is the one finished row with somewhere to go, and a
       finished one worth sharing is the other. Both are offers, so they carry
       their tone where the plain stop control stays quiet. */
    .control.is-retry { background: var(--overlay-danger-1); color: var(--color-danger); }
    .control.is-retry:active { background: var(--overlay-danger-2); }
    .control.is-share { background: var(--overlay-accent-1); color: var(--color-accent); }
    .control.is-share:active { background: var(--overlay-accent-2); }

    @media (prefers-reduced-motion: reduce) {
        .fill { transition: none; }
        .is-indeterminate .fill { width: 100%; animation: none; opacity: 0.4; }
    }
</style>
