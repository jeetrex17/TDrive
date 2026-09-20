<script lang="ts">
    import FolderInputIcon from '@lucide/svelte/icons/folder-input';
    import DownloadIcon from '@lucide/svelte/icons/download';
    import Trash2Icon from '@lucide/svelte/icons/trash-2';
    import { selectionBarState } from './selection-bar-store';

    interface SelectionBarProps {
        onDownload: () => void;
        onMove: () => void;
        onDelete: () => void;
        onClear: () => void;
    }

    let { onDownload, onMove, onDelete, onClear }: SelectionBarProps = $props();

    const label = $derived($selectionBarState.count === 1
        ? '1 selected'
        : `${$selectionBarState.count} selected`);
</script>

<!-- Desktop: count on the left, pill actions on the right (unchanged).
     Mobile: the host becomes a bottom action bar; the count and Done move to
     the top region owned by the shell (which reads selectionBarState.count and
     calls clearSelection), so the count and Clear are hidden here and the bulk
     actions show as labelled icons. -->
<div id="selection-count" class="selection-count" aria-atomic="true">{label}</div>
<div class="selection-actions">
    <button id="selection-download" class="selection-btn" type="button" onclick={onDownload}>
        <span class="selection-btn-icon" aria-hidden="true">
            <DownloadIcon size={22} strokeWidth={1.75} />
        </span>
        <span class="selection-btn-label">Download</span>
    </button>
    <button id="selection-move" class="selection-btn" type="button" onclick={onMove}>
        <span class="selection-btn-icon" aria-hidden="true">
            <FolderInputIcon size={22} strokeWidth={1.75} />
        </span>
        <span class="selection-btn-label">Move</span>
    </button>
    <button id="selection-delete" class="selection-btn danger" type="button" onclick={onDelete}>
        <span class="selection-btn-icon" aria-hidden="true">
            <Trash2Icon size={22} strokeWidth={1.75} />
        </span>
        <span class="selection-btn-label">Delete</span>
    </button>
    <button id="selection-clear" class="selection-btn ghost" type="button" onclick={onClear}>Clear</button>
</div>

<style>
    /* The action glyphs are mobile-only; desktop keeps text pills. */
    .selection-btn-icon { display: none; }

    :global(html.mobile .selection-bar) {
        position: fixed;
        inset: auto 0 0 0;
        height: auto;
        padding: var(--space-2) var(--space-2) calc(var(--space-2) + var(--inset-bottom));
        justify-content: space-around;
        align-items: stretch;
        border-top: 1px solid var(--color-border-soft);
        border-bottom: none;
        background: var(--color-surface-0);
        -webkit-backdrop-filter: none;
        backdrop-filter: none;
    }

    /* The shell renders the count and Done in the top region on a phone. */
    :global(html.mobile) .selection-count { display: none; }
    :global(html.mobile) .selection-actions {
        flex: 1;
        justify-content: space-around;
        gap: var(--space-1);
    }
    :global(html.mobile) .selection-btn.ghost { display: none; }

    :global(html.mobile) .selection-btn {
        flex-direction: column;
        gap: 3px;
        min-width: 64px;
        min-height: 48px;
        height: auto;
        padding: var(--space-1) var(--space-3);
        border: none;
        background: transparent;
        border-radius: var(--radius-md);
        font-size: var(--type-xs);
        font-weight: 600;
    }
    :global(html.mobile) .selection-btn:active { background: var(--overlay-neutral-2); transform: none; }
    :global(html.mobile) .selection-btn.danger { border: none; color: var(--color-danger); }
    :global(html.mobile) .selection-btn.danger:active { background: var(--overlay-danger-1); }
    :global(html.mobile) .selection-btn-icon {
        display: flex;
        align-items: center;
        justify-content: center;
    }
</style>
