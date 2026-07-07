<script lang="ts">
    // Renders the gallery view state. The scroll host (#gallery-view) stays in
    // index.html: it is the IntersectionObserver root and owns click
    // delegation, both wired by modules/gallery.ts against a stable element.
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
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><rect x="3" y="3" width="18" height="18" rx="2"/><circle cx="8.5" cy="8.5" r="1.6"/><path stroke-linecap="round" stroke-linejoin="round" d="M21 15l-5-5L5 21"/></svg>
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
