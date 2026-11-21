<script lang="ts">
    import type { Snippet } from 'svelte';
    import XIcon from '@lucide/svelte/icons/x';

    // The photo, video and document viewers share this bar: name and details
    // on the leading side, actions ending in the same close button. Each owner
    // passes its own class to place the bar and fade it with its chrome.
    interface Props {
        class?: string;
        titleId: string;
        /** Controllers that write the name themselves leave this out. */
        title?: string;
        /** Announce name changes, for viewers that step between items. */
        liveTitle?: boolean;
        closeId?: string;
        closeLabel: string;
        onClose?: () => void;
        leading?: Snippet;
        meta?: Snippet;
        actions?: Snippet;
    }

    let {
        class: className = '',
        titleId,
        title,
        liveTitle = false,
        closeId,
        closeLabel,
        onClose,
        leading,
        meta,
        actions,
    }: Props = $props();
</script>

<div class={['viewer-topbar', className]}>
    {@render leading?.()}
    <div class="viewer-title-group">
        <h2 id={titleId} class="viewer-title" title={title || undefined} aria-live={liveTitle ? 'polite' : undefined}>{title ?? ''}</h2>
        {#if meta}<div class="viewer-meta">{@render meta()}</div>{/if}
    </div>
    <div class="viewer-actions">
        {@render actions?.()}
        <button id={closeId} class="viewer-icon-btn viewer-close" type="button" aria-label={closeLabel} title="Close" onclick={onClose}>
            <XIcon strokeWidth={2.2} aria-hidden="true" />
        </button>
    </div>
</div>
