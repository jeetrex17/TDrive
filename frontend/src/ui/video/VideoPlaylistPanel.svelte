<script lang="ts">
    import XIcon from '@lucide/svelte/icons/x';
    import { onMount, tick } from 'svelte';
    import { formatBytes } from '../../utils';
    import { videoPlaylistStore } from './video-playlist-store';

    interface Props {
        onClose?: () => void;
        onSelect?: (index: number) => void;
        onAutoNextChange?: (enabled: boolean) => void;
    }

    let {
        onClose = () => undefined,
        onSelect = () => undefined,
        onAutoNextChange = () => undefined,
    }: Props = $props();

    let panel = $state<HTMLElement | null>(null);
    let focusedIndex = $state<number | null>(null);
    const playlist = $derived($videoPlaylistStore);
    const tabbableIndex = $derived(
        focusedIndex !== null && focusedIndex >= 0 && focusedIndex < playlist.items.length
            ? focusedIndex
            : playlist.currentIndex >= 0 && playlist.currentIndex < playlist.items.length
                ? playlist.currentIndex
                : playlist.items.length > 0 ? 0 : -1,
    );

    function scrollRowIntoView(owner: HTMLElement, index: number): void {
        const list = owner.querySelector<HTMLElement>('.video-playlist-list');
        const row = owner.querySelector<HTMLElement>(`[data-playlist-index="${index}"]`);
        if (!list || !row) return;

        const listRect = list.getBoundingClientRect();
        const rowRect = row.getBoundingClientRect();
        if (rowRect.top < listRect.top) {
            list.scrollTop -= Math.ceil(listRect.top - rowRect.top);
        } else if (rowRect.bottom > listRect.bottom) {
            list.scrollTop += Math.ceil(rowRect.bottom - listRect.bottom);
        }
    }

    // Keep keyboard focus discoverable as snapshots arrive, without stealing
    // focus from a user who is already moving through the list.
    $effect(() => {
        const itemCount = playlist.items.length;
        if (itemCount === 0) {
            focusedIndex = null;
            return;
        }
        if (focusedIndex === null || focusedIndex < 0 || focusedIndex >= itemCount) {
            focusedIndex = playlist.currentIndex >= 0 && playlist.currentIndex < itemCount
                ? playlist.currentIndex
                : 0;
        }
    });
    onMount(() => {
        let activeIndex = -1;
        let open = false;
        const scrollActiveRow = () => {
            if (open && panel && activeIndex >= 0) scrollRowIntoView(panel, activeIndex);
        };
        const unsubscribe = videoPlaylistStore.subscribe((state) => {
            activeIndex = state.currentIndex;
            open = state.open;
            void tick().then(scrollActiveRow);
        });
        const list = panel?.querySelector<HTMLElement>('.video-playlist-list');
        const observer = list && typeof ResizeObserver !== 'undefined'
            ? new ResizeObserver(scrollActiveRow)
            : null;
        if (list) observer?.observe(list);
        window.addEventListener('resize', scrollActiveRow);

        return () => {
            unsubscribe();
            observer?.disconnect();
            window.removeEventListener('resize', scrollActiveRow);
        };
    });

    function close(): void {
        onClose();
    }

    function select(index: number): void {
        if (index < 0 || index >= playlist.items.length) return;
        onSelect(index);
    }

    function focusRow(index: number): void {
        if (index < 0 || index >= playlist.items.length) return;
        focusedIndex = index;
        void tick().then(() => {
            const currentPanel = panel;
            const row = currentPanel?.querySelector<HTMLButtonElement>(`[data-playlist-index="${index}"]`);
            row?.focus();
            if (currentPanel) scrollRowIntoView(currentPanel, index);
        });
    }

    function moveFocus(event: KeyboardEvent, index: number): void {
        const count = playlist.items.length;
        if (event.key === 'Enter' || event.key === ' ' || event.key === 'Spacebar') {
            event.preventDefault();
            select(index);
            return;
        }
        if (event.key === 'Home' || event.key === 'End' || event.key === 'ArrowUp' || event.key === 'ArrowDown') {
            event.preventDefault();
            event.stopPropagation();
            const next = event.key === 'Home'
                ? 0
                : event.key === 'End'
                    ? count - 1
                    : Math.max(0, Math.min(count - 1, index + (event.key === 'ArrowDown' ? 1 : -1)));
            focusRow(next);
        }
    }

    function handlePanelKeydown(event: KeyboardEvent): void {
        if (event.key !== 'Escape' || !playlist.open) return;
        event.preventDefault();
        event.stopPropagation();
        close();
    }

    function changeAutoNext(event: Event): void {
        const input = event.currentTarget as HTMLInputElement;
        onAutoNextChange(input.checked);
    }

    function activateRow(index: number): void {
        focusedIndex = index;
        select(index);
    }
</script>

<aside
    bind:this={panel}
    id="video-playlist-panel"
    class="video-popover video-playlist-panel"
    data-playlist-panel
    data-state={playlist.open ? 'open' : 'closed'}
    role="region"
    aria-label="Video playlist"
    aria-labelledby="video-playlist-title"
    aria-hidden={playlist.open ? undefined : 'true'}
    tabindex="-1"
    onkeydown={handlePanelKeydown}
>
    <header class="video-popover-header">
        <div class="video-popover-heading">
            <h2 id="video-playlist-title" class="video-popover-title video-playlist-title">{playlist.title}</h2>
            <span class="video-popover-count video-playlist-count" aria-label={`${playlist.items.length} videos`}>
                {playlist.items.length}
            </span>
        </div>
        <button class="video-popover-close video-playlist-close" type="button" aria-label="Close playlist" title="Close playlist" onclick={close}>
            <XIcon size={16} strokeWidth={2} aria-hidden="true" />
        </button>
    </header>

    <div class="video-popover-body video-playlist-list" aria-label="Videos in this playlist">
        {#if playlist.items.length === 0}
            <p class="video-playlist-empty">No videos in this folder.</p>
        {:else}
            {#each playlist.items as item, index (item.id)}
                {@const active = playlist.currentIndex === index}
                {@const switching = playlist.switchingId === item.id}
                <button
                    class="video-playlist-row"
                    class:is-active={active}
                    class:is-switching={switching}
                    type="button"
                    data-playlist-index={index}
                    data-playlist-id={item.id}
                    aria-current={active ? 'true' : undefined}
                    aria-label={`${item.name}, ${item.position} of ${playlist.items.length}, ${item.format}, ${formatBytes(item.size)}${active ? ', currently playing' : ''}`}
                    aria-busy={switching ? 'true' : undefined}
                    tabindex={index === tabbableIndex ? 0 : -1}
                    onclick={() => activateRow(index)}
                    onfocus={() => { focusedIndex = index; }}
                    onkeydown={(event) => moveFocus(event, index)}
                >
                    <span class="video-playlist-index" aria-hidden="true">{item.position}</span>
                    <span class="video-playlist-row-copy">
                        <span class="video-playlist-row-name" title={item.name}>{item.name}</span>
                        <span class="video-playlist-row-meta">
                            <span class="video-playlist-format">{item.format}</span>
                            <span class="video-playlist-size">{formatBytes(item.size)}</span>
                        </span>
                    </span>
                    <span class="video-playlist-row-state" aria-hidden="true">
                        {#if switching}
                            <span class="video-playlist-switching-mark"></span>
                        {:else if active}
                            <span class="video-playlist-playing-mark"><span></span><span></span><span></span></span>
                        {/if}
                    </span>
                </button>
            {/each}
        {/if}
    </div>

    <div class="video-popover-footer">
        <label class="video-playlist-auto-next">
            <span class="video-playlist-auto-next-label">Auto-next</span>
            <input
                class="video-playlist-switch"
                type="checkbox"
                role="switch"
                aria-label="Auto-next"
                aria-checked={playlist.autoNext}
                checked={playlist.autoNext}
                onchange={changeAutoNext}
            />
            <span class="video-playlist-switch-track" aria-hidden="true">
                <span class="video-playlist-switch-thumb"></span>
            </span>
        </label>
    </div>
</aside>

<style>
    .video-playlist-panel {
        --video-popover-max-height: 560px;
    }

    .video-playlist-title {
        font-size: 13px;
    }

    .video-playlist-list {
        padding: var(--video-popover-inset);
    }

    .video-playlist-row {
        width: 100%;
        display: grid;
        grid-template-columns: 20px minmax(0, 1fr) 18px;
        align-items: center;
        gap: 10px;
        min-height: 44px;
        padding: 7px 8px;
        border: 0;
        border-radius: var(--video-popover-item-radius);
        background: transparent;
        color: inherit;
        text-align: left;
        cursor: pointer;
        transition:
            background-color 120ms var(--ease-standard),
            color 120ms var(--ease-standard);
    }

    .video-playlist-row:hover {
        background: rgba(255, 255, 255, 0.06);
    }

    /* Flat tint plus an accent index. Colour carries the state on its own, so
       the equalizer is a second cue rather than the only one. */
    .video-playlist-row.is-active,
    .video-playlist-row.is-switching {
        background: rgba(122, 162, 247, 0.16);
    }

    .video-playlist-index {
        color: #7a889f;
        font-size: 11px;
        font-variant-numeric: tabular-nums;
        font-weight: 650;
        text-align: right;
    }

    .video-playlist-row.is-active .video-playlist-index {
        color: #8fb5ff;
    }

    .video-playlist-row-copy {
        display: grid;
        gap: 3px;
        min-width: 0;
    }

    .video-playlist-row-name {
        overflow: hidden;
        color: #e4eaf6;
        font-size: 12.5px;
        font-weight: 650;
        line-height: 1.3;
        text-overflow: ellipsis;
        white-space: nowrap;
    }

    .video-playlist-row.is-active .video-playlist-row-name {
        color: #dce8ff;
    }

    .video-playlist-row-meta {
        display: flex;
        align-items: center;
        gap: 6px;
        color: #7a889f;
        font-size: 11px;
        line-height: 1.2;
    }

    .video-playlist-row-meta > * + *::before {
        content: '·';
        margin-right: 6px;
        color: #57637a;
    }

    .video-playlist-format {
        text-transform: uppercase;
    }

    .video-playlist-size {
        font-variant-numeric: tabular-nums;
    }

    .video-playlist-row-state {
        display: grid;
        width: 18px;
        height: 18px;
        place-items: center;
    }

    .video-playlist-playing-mark {
        display: flex;
        align-items: center;
        gap: 2px;
        height: 12px;
    }

    .video-playlist-playing-mark > span {
        width: 2px;
        border-radius: 1px;
        background: #8fb5ff;
    }

    .video-playlist-playing-mark > span:nth-child(1) {
        height: 6px;
    }

    .video-playlist-playing-mark > span:nth-child(2) {
        height: 11px;
    }

    .video-playlist-playing-mark > span:nth-child(3) {
        height: 8px;
    }

    .video-playlist-switching-mark {
        width: 12px;
        height: 12px;
        border: 2px solid rgba(143, 181, 255, 0.24);
        border-top-color: #8fb5ff;
        border-radius: 50%;
        animation: video-playlist-spin 760ms linear infinite;
    }

    .video-playlist-empty {
        margin: 0;
        padding: 24px 12px;
        color: #7a889f;
        font-size: 12.5px;
        line-height: 1.5;
        text-align: center;
    }

    .video-playlist-auto-next {
        position: relative;
        display: flex;
        align-items: center;
        justify-content: space-between;
        gap: 12px;
        padding: 6px 8px 6px 10px;
        border-radius: var(--video-popover-item-radius);
        cursor: pointer;
    }

    .video-playlist-auto-next:hover {
        background: rgba(255, 255, 255, 0.05);
    }

    .video-playlist-auto-next-label {
        color: #c8d2e4;
        font-size: 12px;
        font-weight: 600;
    }

    .video-playlist-switch {
        position: absolute;
        width: 1px;
        height: 1px;
        opacity: 0;
    }

    .video-playlist-switch-track {
        flex: 0 0 auto;
        position: relative;
        display: block;
        width: 34px;
        height: 20px;
        border-radius: 999px;
        background: rgba(255, 255, 255, 0.14);
        transition: background-color 140ms var(--ease-standard);
    }

    .video-playlist-switch-thumb {
        position: absolute;
        top: 3px;
        left: 3px;
        width: 14px;
        height: 14px;
        border-radius: 50%;
        background: #cdd6e6;
        transition:
            translate 140ms var(--ease-standard),
            background-color 140ms var(--ease-standard);
    }

    .video-playlist-switch:checked + .video-playlist-switch-track {
        background: #7aa5ff;
    }

    .video-playlist-switch:checked + .video-playlist-switch-track .video-playlist-switch-thumb {
        translate: 14px 0;
        background: #0b0f18;
    }

    .video-playlist-switch:focus-visible + .video-playlist-switch-track {
        outline: 2px solid #91b4ff;
        outline-offset: 2px;
    }

    @keyframes video-playlist-spin {
        to {
            rotate: 360deg;
        }
    }
</style>
