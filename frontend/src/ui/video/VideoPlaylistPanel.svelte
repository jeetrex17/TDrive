<script lang="ts">
    import FilmIcon from '@lucide/svelte/icons/film';
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
    class="video-playlist-panel"
    data-playlist-panel
    data-state={playlist.open ? 'open' : 'closed'}
    role="region"
    aria-label="Video playlist"
    aria-labelledby="video-playlist-title"
    aria-hidden={playlist.open ? undefined : 'true'}
    tabindex="-1"
    onkeydown={handlePanelKeydown}
>
    <header class="video-playlist-header">
        <div class="video-playlist-heading">
            <h2 id="video-playlist-title" class="video-playlist-title">{playlist.title}</h2>
            <span class="video-playlist-count" aria-label={`${playlist.items.length} videos`}>
                {playlist.items.length}
            </span>
        </div>
        <button class="video-playlist-close" type="button" aria-label="Close playlist" title="Close playlist" onclick={close}>
            <XIcon size={18} strokeWidth={2} aria-hidden="true" />
        </button>
    </header>

    <div class="video-playlist-toolbar">
        <label class="video-playlist-auto-next">
            <span class="video-playlist-auto-next-copy">
                <span class="video-playlist-auto-next-label">Auto-next</span>
                <span class="video-playlist-auto-next-note">Continue with the next video</span>
            </span>
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

    <div class="video-playlist-list" aria-label="Videos in this playlist">
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
                    <span class="video-playlist-tile" aria-hidden="true">
                        <FilmIcon size={19} strokeWidth={1.8} />
                        <span class="video-playlist-tile-format">{item.format}</span>
                    </span>
                    <span class="video-playlist-row-copy">
                        <span class="video-playlist-row-name" title={item.name}>{item.name}</span>
                        <span class="video-playlist-row-meta">
                            <span class="video-playlist-position">{item.position} of {playlist.items.length}</span>
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
</aside>

<style>
    .video-playlist-panel {
        position: absolute;
        z-index: 15;
        top: var(--video-panel-top, 76px);
        right: 12px;
        bottom: var(--video-panel-bottom, 108px);
        width: min(360px, calc(100vw - 24px));
        display: flex;
        flex-direction: column;
        min-width: 0;
        min-height: 0;
        overflow: hidden;
        border: 1px solid rgba(255, 255, 255, 0.11);
        border-radius: 16px;
        background: var(--viewer-surface-1, #11151f);
        color: var(--viewer-text, #edf1fa);
        box-shadow: 0 16px 42px rgba(0, 0, 0, 0.42);
        user-select: none;
        transition: opacity 160ms ease, visibility 160ms ease, transform 160ms ease;
    }

    .video-playlist-panel[data-state='closed'] {
        visibility: hidden;
        opacity: 0;
        pointer-events: none;
        transform: translateX(8px);
    }

    .video-playlist-header {
        display: flex;
        align-items: flex-start;
        justify-content: space-between;
        gap: 14px;
        padding: 17px 16px 13px;
        border-bottom: 1px solid rgba(255, 255, 255, 0.08);
    }

    .video-playlist-heading {
        display: flex;
        align-items: baseline;
        gap: 9px;
        min-width: 0;
    }

    .video-playlist-title {
        min-width: 0;
        margin: 0;
        overflow: hidden;
        color: #f3f6ff;
        font-size: 16px;
        font-weight: 800;
        letter-spacing: -0.015em;
        line-height: 1.25;
        text-overflow: ellipsis;
        white-space: nowrap;
    }

    .video-playlist-count {
        flex: 0 0 auto;
        display: inline-grid;
        min-width: 24px;
        height: 22px;
        place-items: center;
        padding: 0 7px;
        border: 1px solid rgba(145, 180, 255, 0.22);
        border-radius: 7px;
        background: rgba(122, 162, 247, 0.12);
        color: #b9d0ff;
        font-size: 11px;
        font-variant-numeric: tabular-nums;
        font-weight: 800;
    }

    .video-playlist-close {
        flex: 0 0 auto;
        display: grid;
        width: 32px;
        height: 32px;
        place-items: center;
        margin: -4px -4px 0 0;
        border: 1px solid transparent;
        border-radius: 9px;
        background: transparent;
        color: #a8b2c5;
        cursor: pointer;
        transition: color 120ms ease, background 120ms ease, border-color 120ms ease;
    }

    .video-playlist-close:hover {
        border-color: rgba(255, 255, 255, 0.11);
        background: rgba(255, 255, 255, 0.07);
        color: #f3f6ff;
    }

    .video-playlist-toolbar {
        padding: 11px 12px 10px;
        border-bottom: 1px solid rgba(255, 255, 255, 0.07);
    }

    .video-playlist-auto-next {
        position: relative;
        display: flex;
        align-items: center;
        justify-content: space-between;
        gap: 14px;
        min-width: 0;
        padding: 9px 8px;
        border-radius: 10px;
        cursor: pointer;
    }

    .video-playlist-auto-next:hover {
        background: rgba(255, 255, 255, 0.045);
    }

    .video-playlist-auto-next-copy {
        display: grid;
        gap: 3px;
        min-width: 0;
    }

    .video-playlist-auto-next-label {
        color: #eaf0ff;
        font-size: 13px;
        font-weight: 750;
    }

    .video-playlist-auto-next-note {
        overflow: hidden;
        color: #8996ae;
        font-size: 11px;
        line-height: 1.3;
        text-overflow: ellipsis;
        white-space: nowrap;
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
        width: 38px;
        height: 22px;
        border: 1px solid rgba(255, 255, 255, 0.2);
        border-radius: 999px;
        background: rgba(255, 255, 255, 0.12);
        transition: background 140ms ease, border-color 140ms ease;
    }

    .video-playlist-switch-thumb {
        position: absolute;
        top: 3px;
        left: 3px;
        width: 14px;
        height: 14px;
        border-radius: 50%;
        background: #bdc8da;
        box-shadow: 0 1px 2px rgba(0, 0, 0, 0.36);
        transition: transform 140ms ease, background 140ms ease;
    }

    .video-playlist-switch:checked + .video-playlist-switch-track {
        border-color: rgba(145, 180, 255, 0.72);
        background: rgba(122, 162, 247, 0.62);
    }

    .video-playlist-switch:checked + .video-playlist-switch-track .video-playlist-switch-thumb {
        transform: translateX(16px);
        background: #fff;
    }

    .video-playlist-switch:focus-visible + .video-playlist-switch-track,
    .video-playlist-close:focus-visible,
    .video-playlist-row:focus-visible {
        outline: 2px solid #91b4ff;
        outline-offset: 2px;
    }

    .video-playlist-list {
        flex: 1 1 auto;
        min-width: 0;
        min-height: 0;
        overflow-x: hidden;
        overflow-y: auto;
        overscroll-behavior: contain;
        padding: 8px;
        scrollbar-color: rgba(145, 180, 255, 0.62) rgba(255, 255, 255, 0.06);
        scrollbar-gutter: stable;
        scrollbar-width: thin;
    }

    .video-playlist-list::-webkit-scrollbar {
        width: 7px;
    }

    .video-playlist-list::-webkit-scrollbar-track {
        border-radius: 999px;
        background: rgba(255, 255, 255, 0.06);
    }

    .video-playlist-list::-webkit-scrollbar-thumb {
        min-height: 28px;
        border: 1px solid #11151f;
        border-radius: 999px;
        background: rgba(145, 180, 255, 0.62);
    }

    .video-playlist-row {
        width: 100%;
        display: grid;
        grid-template-columns: 48px minmax(0, 1fr) 20px;
        align-items: center;
        gap: 11px;
        min-height: 66px;
        padding: 8px 9px;
        border: 1px solid transparent;
        border-radius: 11px;
        background: transparent;
        color: inherit;
        text-align: left;
        cursor: pointer;
        transition: background 120ms ease, border-color 120ms ease, transform 120ms ease;
    }

    .video-playlist-row:hover {
        background: rgba(255, 255, 255, 0.055);
    }

    .video-playlist-row:active {
        transform: scale(0.992);
    }

    .video-playlist-row.is-active {
        border-color: rgba(122, 162, 247, 0.44);
        background: linear-gradient(100deg, rgba(122, 162, 247, 0.19), rgba(122, 162, 247, 0.08));
    }

    .video-playlist-row.is-switching {
        border-color: rgba(145, 180, 255, 0.3);
        background: rgba(122, 162, 247, 0.1);
    }

    .video-playlist-tile {
        display: grid;
        width: 48px;
        height: 42px;
        place-items: center;
        align-content: center;
        gap: 2px;
        border: 1px solid rgba(145, 180, 255, 0.24);
        border-radius: 9px;
        background: linear-gradient(145deg, rgba(122, 162, 247, 0.22), rgba(122, 162, 247, 0.06));
        color: #a9c4ff;
    }

    .video-playlist-tile-format {
        max-width: 42px;
        overflow: hidden;
        color: #c2d5ff;
        font-size: 8px;
        font-variant-numeric: tabular-nums;
        font-weight: 850;
        letter-spacing: 0.06em;
        line-height: 1;
        text-overflow: ellipsis;
        white-space: nowrap;
    }

    .video-playlist-row-copy {
        display: grid;
        gap: 6px;
        min-width: 0;
    }

    .video-playlist-row-name {
        overflow: hidden;
        color: #edf1fa;
        font-size: 13px;
        font-weight: 700;
        line-height: 1.25;
        text-overflow: ellipsis;
        white-space: nowrap;
    }

    .video-playlist-row-meta {
        display: flex;
        flex-wrap: wrap;
        align-items: center;
        gap: 5px 9px;
        color: #98a7c0;
        font-size: 11px;
        line-height: 1.2;
    }

    .video-playlist-position,
    .video-playlist-format,
    .video-playlist-size {
        font-variant-numeric: tabular-nums;
    }

    .video-playlist-format {
        color: #b4caff;
        font-weight: 750;
        letter-spacing: 0.04em;
    }

    .video-playlist-row-state {
        display: grid;
        width: 20px;
        height: 20px;
        place-items: center;
    }

    .video-playlist-playing-mark {
        display: flex;
        align-items: center;
        gap: 2px;
        height: 14px;
    }

    .video-playlist-playing-mark > span {
        width: 2px;
        border-radius: 2px;
        background: #8fb5ff;
    }

    .video-playlist-playing-mark > span:nth-child(1) {
        height: 7px;
    }

    .video-playlist-playing-mark > span:nth-child(2) {
        height: 13px;
    }

    .video-playlist-playing-mark > span:nth-child(3) {
        height: 9px;
    }

    .video-playlist-switching-mark {
        width: 12px;
        height: 12px;
        border: 2px solid rgba(176, 202, 255, 0.28);
        border-top-color: #a9c4ff;
        border-radius: 50%;
        animation: video-playlist-spin 760ms linear infinite;
    }

    .video-playlist-empty {
        margin: 0;
        padding: 26px 14px;
        color: #9caac2;
        font-size: 13px;
        line-height: 1.5;
        text-align: center;
    }

    @keyframes video-playlist-spin {
        to {
            transform: rotate(360deg);
        }
    }

    @media (prefers-reduced-motion: reduce) {
        .video-playlist-panel,
        .video-playlist-close,
        .video-playlist-row,
        .video-playlist-switch-track,
        .video-playlist-switch-thumb {
            transition: none;
        }

        .video-playlist-switching-mark {
            animation: none;
        }
    }

    @media (max-width: 760px) {
        .video-playlist-panel {
            top: auto;
            right: 10px;
            bottom: var(--video-panel-bottom, 108px);
            left: 10px;
            width: auto;
            height: min(360px, 48vh);
        }

        .video-playlist-panel[data-state='closed'] {
            transform: translateY(8px);
        }
    }
</style>
