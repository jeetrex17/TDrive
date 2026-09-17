<script lang="ts">
    import { onMount, tick, untrack } from 'svelte';
    import ImageIcon from '@lucide/svelte/icons/image';
    import { isMobilePlatform } from '../../api';
    import { appActions } from '../../modules/app-actions';
    import { chooseFilesForCurrentFolder } from '../../modules/transfers';
    import GalleryCell from './GalleryCell.svelte';
    import { galleryView } from './gallery-store';
    import { createGalleryLayout, galleryWindow, indexAtOffset, offsetForIndex } from './gallery-layout';

    const mobile = isMobilePlatform();
    const skeletonCells = Array.from({ length: 12 }, (_, index) => index);
    let root = $state<HTMLElement | null>(null);
    let width = $state(mobile ? 390 : 900);
    let height = $state(800);
    let scrollTop = $state(0);
    let version = $state(0);
    let focusedIndex = $state(0);
    let keyboardRequest = 0;
    let source = $derived($galleryView.status === 'ready' ? $galleryView.source : null);
    let layout = $derived(createGalleryLayout(source?.timeline.buckets ?? [], width, mobile));
    let visible = $derived(galleryWindow(layout, scrollTop, height));
    let rows = $derived.by(() => {
        void version;
        return visible.rows.map((row) => ({ ...row, cells: row.indices.map((index) => ({ index, item: source?.peek(index) })) }));
    });
    let firstIndex = $derived(visible.rows[0]?.indices[0] ?? 0);
    let lastIndex = $derived.by(() => { const indices = visible.rows[visible.rows.length - 1]?.indices; return indices?.[indices.length - 1] ?? 0; });
    let tabIndex = $derived(focusedIndex >= firstIndex && focusedIndex <= lastIndex ? focusedIndex : firstIndex);
    let pageError = $derived.by(() => { void version; return source?.error ?? ''; });

    $effect(() => {
        const current = source;
        if (!current) return;
        return current.subscribe(() => { version += 1; });
    });
    $effect(() => { source?.ensureRange(firstIndex, lastIndex); });
    $effect(() => {
        const view = $galleryView;
        if (!root || view.status !== 'ready') return;
        // Restoring a file anchor absorbs inserts/deletes ahead of it.
        const top = untrack(() => view.initialIndex === undefined ? root!.scrollTop : Math.max(0, offsetForIndex(layout, view.initialIndex) + (view.anchorOffset ?? 0)));
        root.scrollTop = top;
        scrollTop = top;
    });

    function measure(): void {
        if (!root || root.clientWidth === 0) return;
        const style = getComputedStyle(root);
        const nextWidth = Math.max(1, root.clientWidth - parseFloat(style.paddingLeft || '0') - parseFloat(style.paddingRight || '0'));
        const anchor = indexAtOffset(layout, root.scrollTop);
        const withinRow = root.scrollTop - offsetForIndex(layout, anchor);
        if (nextWidth !== width) {
            const nextLayout = createGalleryLayout(source?.timeline.buckets ?? [], nextWidth, mobile);
            width = nextWidth;
            root.scrollTop = Math.max(0, offsetForIndex(nextLayout, anchor) + withinRow);
        }
        height = root.clientHeight || 800;
        scrollTop = root.scrollTop;
    }

    async function onKeydown(event: KeyboardEvent): Promise<void> {
        if (!source || event.isComposing || event.altKey || event.metaKey || event.ctrlKey) return;
        const cell = (event.target as HTMLElement).closest<HTMLElement>('button.gallery-cell');
        if (!cell && event.target !== root) return;
        const index = Number(cell?.dataset.index ?? focusedIndex);
        const offsets: Record<string, number> = { ArrowLeft: -1, ArrowRight: 1, ArrowUp: -layout.columns, ArrowDown: layout.columns, PageUp: -layout.columns * Math.max(1, Math.floor(height / layout.rowPitch)), PageDown: layout.columns * Math.max(1, Math.floor(height / layout.rowPitch)) };
        const next = event.key === 'Home' ? 0 : event.key === 'End' ? source.timeline.totalCount - 1 : index + (offsets[event.key] ?? 0);
        if (!(event.key in offsets) && event.key !== 'Home' && event.key !== 'End') return;
        event.preventDefault();
        const request = ++keyboardRequest;
        const target = Math.max(0, Math.min(source.timeline.totalCount - 1, next));
        const current = source;
        focusedIndex = target;
        const top = offsetForIndex(layout, target);
        if (root && (top < root.scrollTop + layout.headerHeight || top + layout.cellSize > root.scrollTop + height)) {
            root.scrollTop = Math.max(0, top - layout.headerHeight);
            scrollTop = root.scrollTop;
        }
        try { await current.get(target); } catch { return; }
        if (source !== current || request !== keyboardRequest) return;
        version += 1;
        await tick();
        root?.querySelector<HTMLElement>(`.gallery-cell[data-index="${target}"]`)?.focus({ preventScroll: true });
    }

    function onFocus(event: FocusEvent): void {
        const cell = (event.target as HTMLElement).closest<HTMLElement>('.gallery-cell');
        if (cell) focusedIndex = Number(cell.dataset.index);
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
                const anchor = indexAtOffset(layout, scrollTop);
                host.dataset.anchorIndex = String(anchor);
                host.dataset.anchorOffset = String(scrollTop - offsetForIndex(layout, anchor));
                // Retain keyboard ownership when its focused row scrolls away.
                const cell = document.activeElement?.closest<HTMLElement>('.gallery-cell');
                if (cell && host.contains(cell)) {
                    const top = offsetForIndex(layout, Number(cell.dataset.index));
                    if (top + layout.cellSize < scrollTop - layout.rowPitch * 2 || top > scrollTop + height + layout.rowPitch * 2) host.focus({ preventScroll: true });
                }
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

{#if $galleryView.status === 'loading'}
    {#if mobile}
        <div class="gallery-grid gallery-skeleton" role="status" aria-label="Loading photos" aria-busy="true">
            {#each skeletonCells as index (index)}
                <div class="gallery-skeleton-cell" style={`--skeleton-delay: ${index * 45}ms`} aria-hidden="true"></div>
            {/each}
        </div>
    {:else}
        <div class="gallery-status" role="status">Loading photos…</div>
    {/if}
{:else if $galleryView.status === 'error'}
    <div class="gallery-empty" role="alert">
        <div class="gallery-empty-title">Could not load photos.</div>
        <div class="gallery-empty-sub">Check your connection and try again.</div>
        <div class="gallery-empty-actions"><button class="primary-btn" type="button" onclick={() => appActions().refreshFiles()}>Retry</button></div>
    </div>
{:else if $galleryView.status === 'empty'}
    <div class="gallery-empty">
        <div class="gallery-empty-icon"><ImageIcon size={48} strokeWidth={1.5} aria-hidden="true" /></div>
        <div class="gallery-empty-title">{mobile ? 'No photos in this drive.' : 'No photos yet'}</div>
        {#if mobile}
            <div class="gallery-empty-actions"><button class="primary-btn" type="button" onclick={() => chooseFilesForCurrentFolder()}>Upload photos</button></div>
        {:else}
            <div class="gallery-empty-sub">Images you upload to this drive show up here.</div>
        {/if}
    </div>
{:else}
    {#key $galleryView.source.timeline.channelId}
    <div class="gallery-virtual" role="grid" aria-label="Photos" aria-rowcount={layout.rowCount} aria-colcount={layout.columns} style:height={`${layout.height}px`}>
        {#each rows as row (row.key)}
            <div class="gallery-grid gallery-virtual-row" role="row" aria-rowindex={row.rowIndex} style:top={`${row.top}px`} style:height={`${layout.cellSize}px`} style:grid-template-columns={`repeat(${layout.columns}, minmax(0, 1fr))`}>
                {#each row.cells as cell (cell.item?.msgId ?? `pending:${cell.index}`)}
                    <div role="gridcell" class="gallery-grid-cell">
                        {#if cell.item}
                            <GalleryCell item={cell.item} index={cell.index} tabindex={cell.index === tabIndex ? 0 : -1} />
                        {:else}
                            <div class="gallery-cell gallery-pending" aria-label="Loading photo" aria-busy="true"></div>
                        {/if}
                    </div>
                {/each}
            </div>
        {/each}
        {#each visible.headers as header (header.key)}
            <div class="gallery-group-header gallery-virtual-header" style:top={`${header.top}px`} style:height={`${layout.headerHeight}px`}>{header.label}</div>
        {/each}
    </div>
    {/key}
    {#if pageError}<div class="gallery-page-error" role="status">{pageError} <button class="secondary-btn" type="button" onclick={() => source?.ensureRange(firstIndex, lastIndex)}>Retry</button></div>{/if}
{/if}
