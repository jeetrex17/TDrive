<script lang="ts">
    // The album grid: one screen of folders instead of one endless timeline.
    // It windows by row the way the file list does, so a drive with hundreds of
    // media folders costs a viewport of DOM rather than a tile per folder.
    import { onMount, tick } from 'svelte';
    import { rowOffset, rowWindowFor } from '../file-list/row-window';
    import AlbumTileView from './AlbumTile.svelte';
    import { albumsView } from './gallery-store';
    import { showPhotos } from '../../modules/gallery';
    import {
        ALBUM_LABEL_HEIGHT, ALBUM_TILE_GAP, albumGridMetrics, albumRowMetrics, type AlbumTile,
    } from './album-view';

    /** Rows of slack either side, so a flick lands on something already drawn. */
    const OVERSCAN = 2;
    const SKELETON_TILES = 8;

    let root = $state<HTMLElement | null>(null);
    let width = $state(900);
    let height = $state(800);
    let scrollTop = $state(0);
    let focusedIndex = $state(0);

    const tiles = $derived($albumsView.status === 'ready' ? $albumsView.tiles : []);
    const metrics = $derived(albumGridMetrics(width, tiles.length));
    const rowMetrics = $derived(albumRowMetrics(metrics));
    const window_ = $derived(rowWindowFor(metrics.rowCount, scrollTop, height, OVERSCAN, rowMetrics));
    const rows = $derived.by(() => {
        const built: Array<{ key: number; tiles: Array<{ tile: AlbumTile; index: number }> }> = [];
        for (let row = window_.start; row < window_.end; row += 1) {
            const first = row * metrics.columns;
            built.push({
                key: row,
                tiles: tiles.slice(first, first + metrics.columns).map((tile, offset) => ({ tile, index: first + offset })),
            });
        }
        return built;
    });
    // Roving tabindex: one stop for the whole grid, and it is a tile that is
    // actually rendered, so Tab can never land on nothing.
    const firstRendered = $derived(window_.start * metrics.columns);
    const lastRendered = $derived(Math.min(tiles.length, window_.end * metrics.columns) - 1);
    const tabIndex = $derived(focusedIndex >= firstRendered && focusedIndex <= lastRendered ? focusedIndex : firstRendered);

    function open(tile: AlbumTile): void {
        void showPhotos({ kind: 'album', tile });
    }

    function measure(): void {
        if (!root || root.clientWidth === 0) return;
        const style = getComputedStyle(root);
        width = Math.max(1, root.clientWidth - parseFloat(style.paddingLeft || '0') - parseFloat(style.paddingRight || '0'));
        height = root.clientHeight || 800;
        scrollTop = root.scrollTop;
    }

    async function onKeydown(event: KeyboardEvent): Promise<void> {
        if (event.isComposing || event.altKey || event.metaKey || event.ctrlKey || tiles.length === 0) return;
        const tile = (event.target as HTMLElement).closest<HTMLElement>('button.album-tile');
        if (!tile) return;
        const index = Number(tile.dataset.index ?? focusedIndex);
        const perScreen = metrics.columns * Math.max(1, Math.floor(height / metrics.rowPitch));
        const steps: Record<string, number> = {
            ArrowLeft: -1, ArrowRight: 1,
            ArrowUp: -metrics.columns, ArrowDown: metrics.columns,
            PageUp: -perScreen, PageDown: perScreen,
        };
        const step = steps[event.key];
        if (step === undefined && event.key !== 'Home' && event.key !== 'End') return;
        event.preventDefault();
        const target = Math.max(0, Math.min(tiles.length - 1, event.key === 'Home' ? 0 : event.key === 'End' ? tiles.length - 1 : index + step));
        focusedIndex = target;
        // Scroll first: a tile outside the window has no element to focus yet.
        const top = rowOffset(Math.floor(target / metrics.columns), rowMetrics);
        if (root && (top < scrollTop || top + metrics.rowPitch > scrollTop + height)) {
            root.scrollTop = top < scrollTop ? top : top + metrics.rowPitch - height;
            scrollTop = root.scrollTop;
        }
        await tick();
        root?.querySelector<HTMLElement>(`.album-tile[data-index="${target}"]`)?.focus({ preventScroll: true });
    }

    function onFocus(event: FocusEvent): void {
        const tile = (event.target as HTMLElement).closest<HTMLElement>('.album-tile');
        if (tile) focusedIndex = Number(tile.dataset.index);
    }

    onMount(() => {
        root = document.getElementById('gallery-view');
        if (!root) return;
        const host = root;
        let frame = 0;
        const onScroll = () => {
            if (frame) return;
            frame = requestAnimationFrame(() => {
                frame = 0;
                scrollTop = host.scrollTop;
            });
        };
        measure();
        const observer = typeof ResizeObserver === 'undefined' ? null : new ResizeObserver(measure);
        observer?.observe(host);
        host.addEventListener('scroll', onScroll, { passive: true });
        host.addEventListener('keydown', onKeydown);
        host.addEventListener('focusin', onFocus);
        window.addEventListener('resize', measure);
        return () => {
            cancelAnimationFrame(frame);
            observer?.disconnect();
            host.removeEventListener('scroll', onScroll);
            host.removeEventListener('keydown', onKeydown);
            host.removeEventListener('focusin', onFocus);
            window.removeEventListener('resize', measure);
        };
    });
</script>

<div class="album-grid-root" style:--album-label-height={`${ALBUM_LABEL_HEIGHT}px`} style:--album-gap={`${ALBUM_TILE_GAP}px`}>
    {#if $albumsView.status === 'loading'}
        <div class="album-row" role="status" aria-label="Loading albums" aria-busy="true" style:grid-template-columns={`repeat(${metrics.columns}, minmax(0, 1fr))`}>
            {#each Array.from({ length: SKELETON_TILES }, (_, index) => index) as index (index)}
                <span class="album-cover is-loading" aria-hidden="true"></span>
            {/each}
        </div>
    {:else}
        <div class="album-list" role="list" aria-label="Albums">
            <div style:height={`${window_.before}px`} aria-hidden="true"></div>
            {#each rows as row (row.key)}
                <!-- The row is layout, not structure: presentation keeps the
                     list owning its items across the windowing wrappers. -->
                <div class="album-row" role="presentation" style:grid-template-columns={`repeat(${metrics.columns}, minmax(0, 1fr))`}>
                    {#each row.tiles as entry (entry.tile.folderId)}
                        <div role="listitem" aria-setsize={tiles.length} aria-posinset={entry.index + 1}>
                            <AlbumTileView
                                tile={entry.tile}
                                index={entry.index}
                                tabindex={entry.index === tabIndex ? 0 : -1}
                                onOpen={open}
                            />
                        </div>
                    {/each}
                </div>
            {/each}
            <div style:height={`${window_.after}px`} aria-hidden="true"></div>
        </div>
    {/if}
</div>
