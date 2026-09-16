<script lang="ts">
    // Renders the gallery view state. The scroll host (#gallery-view) stays in
    // index.html; this component windows complete grid-row chunks so large
    // libraries do not create one component and observer target per image.
    import { onMount } from 'svelte';
    import { SvelteMap, SvelteSet } from 'svelte/reactivity';
    import ImageIcon from '@lucide/svelte/icons/image';
    import { isMobilePlatform } from '../../api';
    import { appActions } from '../../modules/app-actions';
    import { chooseFilesForCurrentFolder } from '../../modules/transfers';
    import GalleryCell from './GalleryCell.svelte';
    import { galleryView, type GalleryCellModel, type GalleryGroup } from './gallery-store';

    interface GalleryChunk {
        key: string;
        cells: GalleryCellModel[];
        height: number;
    }

    // The phone grid is denser (three cells across at 390pt) and its cells sit
    // 2px apart, so the chunk geometry has to follow gallery.css.
    const mobile = isMobilePlatform();
    const MIN_CELL_WIDTH = mobile ? 112 : 172;
    const GRID_GAP = mobile ? 2 : 5;
    const ROWS_PER_CHUNK = 8;
    const SKELETON_CELLS = Array.from({ length: 12 }, (_, index) => index);

    let columnCount = $state(1);
    let gridWidth = $state(MIN_CELL_WIDTH);
    let windowingEnabled = $state(typeof window !== 'undefined' && typeof IntersectionObserver !== 'undefined');
    const visibleChunkKeys = new SvelteSet<string>();
    let chunkObserver: IntersectionObserver | null = null;
    const chunkNodes = new SvelteMap<HTMLElement, string>();

    function updateGeometry(root: HTMLElement): void {
        // A hidden gallery measures zero wide, and acting on that would drop the
        // grid to one column: every chunk key changes, so the cells unmount and
        // re-download their thumbnails on the way back. Keep the last real
        // geometry until the view is laid out again.
        if (root.clientWidth === 0) return;
        const style = getComputedStyle(root);
        const horizontalPadding = Number.parseFloat(style.paddingLeft || '0') + Number.parseFloat(style.paddingRight || '0');
        const nextWidth = Math.max(1, Math.round(root.clientWidth - horizontalPadding));
        const nextColumns = Math.max(1, Math.floor((nextWidth + GRID_GAP) / (MIN_CELL_WIDTH + GRID_GAP)));
        if (nextWidth === gridWidth && nextColumns === columnCount) return;
        gridWidth = nextWidth;
        if (nextColumns !== columnCount) {
            columnCount = nextColumns;
            visibleChunkKeys.clear();
        }
    }

    function chunksFor(group: GalleryGroup, groupIndex: number): GalleryChunk[] {
        const chunkSize = columnCount * ROWS_PER_CHUNK;
        const cellWidth = (gridWidth - GRID_GAP * (columnCount - 1)) / columnCount;
        const chunks: GalleryChunk[] = [];
        for (let start = 0; start < group.cells.length; start += chunkSize) {
            const cells = group.cells.slice(start, start + chunkSize);
            const rows = Math.ceil(cells.length / columnCount);
            chunks.push({
                key: `${groupIndex}:${start}:${columnCount}`,
                cells,
                height: Math.max(0, rows * cellWidth + Math.max(0, rows - 1) * GRID_GAP),
            });
        }
        return chunks;
    }

    function chunkIsVisible(key: string, groupIndex: number, chunkIndex: number): boolean {
        return !windowingEnabled || visibleChunkKeys.has(key) || (groupIndex === 0 && chunkIndex < 2);
    }

    function observeChunk(node: HTMLElement, key: string) {
        chunkNodes.set(node, key);
        chunkObserver?.observe(node);
        return {
            update(nextKey: string) {
                if (nextKey === key) return;
                key = nextKey;
                chunkNodes.set(node, key);
            },
            destroy() {
                chunkObserver?.unobserve(node);
                chunkNodes.delete(node);
            },
        };
    }

    onMount(() => {
        const root = document.getElementById('gallery-view');
        if (!root) return;
        updateGeometry(root);

        if (typeof IntersectionObserver === 'undefined') {
            windowingEnabled = false;
        } else {
            chunkObserver = new IntersectionObserver((entries) => {
                for (const entry of entries) {
                    const node = entry.target as HTMLElement;
                    const key = chunkNodes.get(node);
                    if (!key) continue;
                    if (entry.isIntersecting) {
                        visibleChunkKeys.add(key);
                    } else if (!node.contains(document.activeElement)) {
                        visibleChunkKeys.delete(key);
                    }
                }
            }, { root, rootMargin: '900px 0px' });
            for (const node of chunkNodes.keys()) chunkObserver.observe(node);
        }

        const resizeObserver = typeof ResizeObserver === 'undefined'
            ? null
            : new ResizeObserver(() => updateGeometry(root));
        resizeObserver?.observe(root);
        const onResize = () => updateGeometry(root);
        window.addEventListener('resize', onResize);

        return () => {
            window.removeEventListener('resize', onResize);
            resizeObserver?.disconnect();
            chunkObserver?.disconnect();
            chunkObserver = null;
            chunkNodes.clear();
        };
    });
</script>

{#if $galleryView.status === 'loading'}
    {#if mobile}
        <div class="gallery-grid gallery-skeleton" role="status" aria-label="Loading photos" aria-busy="true">
            {#each SKELETON_CELLS as index (index)}
                <div class="gallery-skeleton-cell" style={`--skeleton-delay: ${index * 45}ms`} aria-hidden="true"></div>
            {/each}
        </div>
    {:else}
        <div class="gallery-status">Loading photos…</div>
    {/if}
{:else if $galleryView.status === 'error'}
    {#if mobile}
        <div class="gallery-empty" role="alert">
            <div class="gallery-empty-title">Could not load photos.</div>
            <div class="gallery-empty-sub">Check your connection and try again.</div>
            <div class="gallery-empty-actions">
                <button class="primary-btn" type="button" onclick={() => appActions().refreshFiles()}>Retry</button>
            </div>
        </div>
    {:else}
        <div class="gallery-status">Could not load photos.</div>
    {/if}
{:else if $galleryView.status === 'empty'}
    <div class="gallery-empty">
        <div class="gallery-empty-icon">
            <ImageIcon size={48} strokeWidth={1.5} aria-hidden="true" />
        </div>
        {#if mobile}
            <div class="gallery-empty-title">No photos in this drive.</div>
            <div class="gallery-empty-actions">
                <button class="primary-btn" type="button" onclick={() => chooseFilesForCurrentFolder()}>Upload photos</button>
            </div>
        {:else}
            <div class="gallery-empty-title">No photos yet</div>
            <div class="gallery-empty-sub">Images you upload to this drive show up here.</div>
        {/if}
    </div>
{:else}
    {#each $galleryView.groups as group, groupIndex (group.cells[0].index)}
        <section class="gallery-group">
            <div class="gallery-group-header">{group.label}</div>
            {#each chunksFor(group, groupIndex) as chunk, chunkIndex (chunk.key)}
                <div class="gallery-window-chunk" use:observeChunk={chunk.key}>
                    {#if chunkIsVisible(chunk.key, groupIndex, chunkIndex)}
                        <div class="gallery-grid">
                            {#each chunk.cells as cell (cell.item.msgId)}
                                <GalleryCell item={cell.item} index={cell.index} />
                            {/each}
                        </div>
                    {:else}
                        <div class="gallery-window-placeholder" style:height={`${chunk.height}px`} aria-hidden="true"></div>
                    {/if}
                </div>
            {/each}
        </section>
    {/each}
{/if}
