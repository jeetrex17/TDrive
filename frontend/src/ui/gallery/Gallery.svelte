<script lang="ts">
    // Renders the gallery view state. The scroll host (#gallery-view) stays in
    // index.html: it is the IntersectionObserver root and owns click
    // delegation, both wired by modules/gallery.ts against a stable element.
    import ImageIcon from '@lucide/svelte/icons/image';
    import GalleryCell from './GalleryCell.svelte';
    import { galleryView } from './gallery-store';
</script>

{#if $galleryView.status === 'loading'}
    <div class="gallery-status">Loading photos…</div>
{:else if $galleryView.status === 'error'}
    <div class="gallery-status">Could not load photos.</div>
{:else if $galleryView.status === 'empty'}
    <div class="gallery-empty">
        <div class="gallery-empty-icon">
            <ImageIcon size={48} strokeWidth={1.5} aria-hidden="true" />
        </div>
        <div class="gallery-empty-title">No photos yet</div>
        <div class="gallery-empty-sub">Images you upload to this drive show up here.</div>
    </div>
{:else}
    <!-- Keyed on the group's first flat index: unique and stable even when two
         non-adjacent groups share a label (e.g. scattered undated photos). -->
    {#each $galleryView.groups as group (group.cells[0].index)}
        <section class="gallery-group">
            <div class="gallery-group-header">{group.label}</div>
            <div class="gallery-grid">
                {#each group.cells as cell (cell.item.msgId)}
                    <GalleryCell item={cell.item} index={cell.index} />
                {/each}
            </div>
        </section>
    {/each}
{/if}
