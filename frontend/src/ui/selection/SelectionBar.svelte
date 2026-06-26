<script lang="ts">
    import { selectionBarState } from './selection-bar-store';

    interface SelectionBarProps {
        onMove: () => void;
        onDelete: () => void;
        onClear: () => void;
    }

    let { onMove, onDelete, onClear }: SelectionBarProps = $props();

    const label = $derived($selectionBarState.count === 1
        ? '1 selected'
        : `${$selectionBarState.count} selected`);
</script>

<div id="selection-count" class="selection-count" aria-atomic="true">{label}</div>
<div class="selection-actions">
    <button id="selection-move" class="selection-btn" type="button" onclick={onMove}>Move</button>
    <button id="selection-delete" class="selection-btn danger" type="button" onclick={onDelete}>Delete</button>
    <button id="selection-clear" class="selection-btn ghost" type="button" onclick={onClear}>Clear</button>
</div>
