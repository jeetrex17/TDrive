<script lang="ts">
    import PlusIcon from '@lucide/svelte/icons/plus';
    import ChannelAvatar from './ChannelAvatar.svelte';
    import type { ChannelSource } from './channel-model';
    import type { ChannelSourcesState } from './channel-store';

    interface Props {
        sources: ChannelSourcesState;
        activeId: number | null;
        loadPhoto: (source: ChannelSource) => Promise<string>;
        onSelect: (source: ChannelSource) => void;
        onActions: (x: number, y: number, source: ChannelSource) => void;
        onAdd: () => void;
        onRetry: () => void;
    }

    let { sources, activeId, loadPhoto, onSelect, onActions, onAdd, onRetry }: Props = $props();

    function openActions(event: MouseEvent, source: ChannelSource): void {
        event.preventDefault();
        onActions(event.clientX, event.clientY, source);
    }
</script>

<div class="channel-nav-title">
    <span>Channels</span>
    <button class="channel-nav-add" type="button" title="Add a channel" aria-label="Add a channel" onclick={onAdd}>
        <PlusIcon size={14} strokeWidth={2.25} aria-hidden="true" />
    </button>
</div>

<div class="drives-list">
    {#each sources.sources as source (source.channelId)}
        {@const active = source.channelId === activeId}
        <button
            class="drive-item channel-nav-item"
            class:active
            type="button"
            title={source.title}
            aria-current={active ? 'page' : undefined}
            onclick={() => onSelect(source)}
            oncontextmenu={(event) => openActions(event, source)}
        >
            <ChannelAvatar {source} {loadPhoto} />
            <span class="drive-item-title">{source.title}</span>
        </button>
    {:else}
        {#if sources.status === 'error'}
            <div class="drive-empty channel-nav-error">
                Channels did not load.
                <button class="link-button" type="button" onclick={onRetry}>Retry</button>
            </div>
        {:else if sources.status === 'ready'}
            <button class="drive-item channel-nav-item" type="button" onclick={onAdd}>
                <span class="channel-nav-add-icon" aria-hidden="true"><PlusIcon size={12} strokeWidth={2.5} /></span>
                <span class="drive-item-title">Add a channel</span>
            </button>
        {/if}
    {/each}
</div>

<style>
    .channel-nav-title {
        display: flex;
        align-items: center;
        justify-content: space-between;
        gap: var(--space-2);
        padding: 0 6px 6px 12px;
        color: var(--text-muted);
        font-size: var(--type-xs);
        font-weight: var(--weight-semibold);
    }

    .channel-nav-add {
        display: inline-grid;
        width: 22px;
        height: 22px;
        place-items: center;
        border: 0;
        border-radius: var(--radius-sm);
        color: var(--text-muted);
        background: transparent;
        cursor: pointer;
        transition:
            background-color var(--motion-fast) var(--ease-standard),
            color var(--motion-fast) var(--ease-standard);
    }

    .channel-nav-add:hover {
        color: var(--text-main);
        background: var(--bg-panel);
    }

    .channel-nav-add:focus-visible {
        outline: none;
        box-shadow: var(--focus-ring);
    }

    /* The channel's own picture takes the drive row's icon column. The gap
       gives back the 2px the avatar is wider than an icon, so channel names
       start where drive names do. */
    .channel-nav-item {
        --avatar-size: 20px;
        gap: 10px;
    }

    .channel-nav-add-icon {
        width: 20px;
        height: 20px;
        flex: 0 0 auto;
        display: grid;
        place-items: center;
        border-radius: var(--radius-pill);
        color: var(--text-muted);
        box-shadow: inset 0 0 0 1px var(--border);
    }

    .channel-nav-item:focus-visible {
        outline: none;
        box-shadow: var(--focus-ring);
    }

    .channel-nav-error {
        display: flex;
        align-items: baseline;
        gap: var(--space-2);
        opacity: 1;
    }

    /* In the drive switcher the rows are touch-sized by the sheet, and the
       title row reads as the group's label with the add action beside it. */
    /* The 6px on the right is the add button's enlarged hit area: inside the
       row it lines the glyph up with the drive rows' action buttons, and it
       keeps the switcher from scrolling sideways. */
    :global(html.mobile) .channel-nav-title {
        padding: var(--space-4) 6px var(--space-1) 12px;
        font-size: var(--mobile-type-meta);
    }

    :global(html.mobile) .channel-nav-add {
        position: relative;
        width: 32px;
        height: 32px;
    }

    :global(html.mobile) .channel-nav-add::after {
        position: absolute;
        inset: -6px;
        content: '';
    }

    :global(html.mobile) .channel-nav-item {
        --avatar-size: 28px;
        gap: 12px;
    }

    :global(html.mobile) .channel-nav-add-icon {
        width: 28px;
        height: 28px;
    }

    @media (prefers-reduced-motion: reduce) {
        .channel-nav-add {
            transition: none;
        }
    }
</style>
