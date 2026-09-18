<script lang="ts">
    import XIcon from '@lucide/svelte/icons/x';
    import type { TransferItem } from './notif-store';
    import { transferItemDetail, transferItemLabel } from './transfer-view';

    /**
     * The files an aggregate transfer has in flight, listed under its row.
     *
     * The bell's row and the phone's row are deliberately separate components --
     * they want different shapes and each states its own -- but a file under
     * either of them wants the same three things and the same order: what it is,
     * how much of it has gone, and how far along that is. So this one is shared,
     * and the two shells size it through the custom properties below rather than
     * through a platform check.
     */
    interface Props {
        items: readonly TransferItem[];
        /** How many are moving in all; anything past the list is summarised. */
        active?: number;
        /** Stops one file. Absent where the backend cannot stop a file alone. */
        onCancel?: (key: string) => void;
    }

    let { items, active = 0, onCancel }: Props = $props();

    const hidden = $derived(Math.max(0, active - items.length));
</script>

{#if items.length > 0}
    <ul class="items">
        {#each items as item (item.key)}
            <li class="item">
                <span class="item-name" title={item.name}>{item.name}</span>
                <span class="item-detail">{transferItemDetail(item)}</span>
                {#if onCancel}
                    <button
                        class="item-stop"
                        type="button"
                        aria-label={`Stop ${item.name || 'this file'}`}
                        onclick={() => onCancel(item.key)}
                    >
                        <XIcon size={13} strokeWidth={2} aria-hidden="true" />
                    </button>
                {/if}
                <!-- Reports itself when queried, without a live region that would
                     announce every tick of every file in the batch. -->
                <div
                    class="item-track"
                    role="progressbar"
                    aria-label={transferItemLabel(item)}
                    aria-valuemin="0"
                    aria-valuemax="100"
                    aria-valuenow={Math.round(Math.max(0, Math.min(100, item.progress)))}
                >
                    <div class="item-fill" style={`width:${Math.max(0, Math.min(100, item.progress))}%`} aria-hidden="true"></div>
                </div>
            </li>
        {/each}
        {#if hidden > 0}
            <!-- The list is capped for reading, so it says what it left out
                 rather than quietly showing four of eight. -->
            <li class="item-more">+{hidden} more {hidden === 1 ? 'file' : 'files'}</li>
        {/if}
    </ul>
{/if}

<style>
    /* The fallbacks below are the desktop sizes; a row that wants other ones
       declares --transfer-item-* on the element it puts this list in, which is
       how the phone gets its own type scale and its thumb-sized stop control.
       They are fallbacks rather than declarations here on purpose: a default
       declared on this list would override the value inherited from the row. */
    .items {
        list-style: none;
        margin: 0;
        padding: 0;
        display: flex;
        flex-direction: column;
        gap: var(--transfer-item-gap, 5px);
        min-width: 0;
    }

    /* Name and figure on one line, the bar beneath spanning both: the bar is
       about the file as a whole, not about either column. */
    .item {
        display: grid;
        grid-template-columns: minmax(0, 1fr) auto auto;
        align-items: center;
        /* Name and bar sit together at the top, so any height the stop control
           adds becomes separation from the next file rather than a gap between
           a file and its own bar. */
        align-content: start;
        column-gap: 8px;
        row-gap: 3px;
        min-width: 0;
        min-height: var(--transfer-item-min-height, 0);
    }

    .item-name {
        min-width: 0;
        font-size: var(--transfer-item-name-size, 0.76rem);
        color: var(--color-text-soft);
        overflow: hidden;
        text-overflow: ellipsis;
        white-space: nowrap;
    }

    .item-detail {
        font-size: var(--transfer-item-meta-size, 0.7rem);
        color: var(--color-text-muted);
        font-variant-numeric: tabular-nums;
        white-space: nowrap;
    }

    .item-track {
        grid-column: 1 / -1;
        height: 3px;
        border-radius: var(--radius-pill);
        background: var(--overlay-neutral-2);
        overflow: hidden;
    }

    /* Quieter than the aggregate bar above it. Two bars at the same weight read
       as two transfers rather than as one and its parts. */
    .item-fill {
        height: 100%;
        border-radius: inherit;
        background: var(--color-accent);
        opacity: 0.62;
        transition: width var(--motion-med) var(--ease-standard);
    }

    /* Pulled back out of the name's line, the way the phone's row does with its
       own control: a thumb-sized target left in the flow would set that line's
       height and strand the bar a long way below the file it belongs to. The
       item's min-height, not the control, is what spaces the files apart. */
    .item-stop {
        grid-row: 1;
        grid-column: 3;
        margin-block: var(--transfer-item-stop-inset, 0);
        display: grid;
        place-items: center;
        width: var(--transfer-item-stop-size, 22px);
        height: var(--transfer-item-stop-size, 22px);
        padding: 0;
        border: 0;
        border-radius: var(--radius-sm);
        background: transparent;
        color: var(--color-text-muted);
        cursor: pointer;
    }
    .item-stop:hover { background: var(--overlay-danger-1); color: var(--color-danger); }
    .item-stop:active { background: var(--overlay-danger-2); }
    .item-stop:focus-visible {
        outline: 2px solid var(--color-accent);
        outline-offset: -2px;
    }

    .item-more {
        font-size: var(--transfer-item-meta-size, 0.7rem);
        color: var(--color-text-subtle);
    }

    @media (prefers-reduced-motion: reduce) {
        .item-fill { transition: none; }
    }
</style>
