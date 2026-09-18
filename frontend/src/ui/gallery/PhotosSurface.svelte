<script lang="ts">
    // What fills the gallery's scroll container. The album grid and the photo
    // timeline share that one host -- its id is the thumbnail controller's
    // observer root and the preview modal's way back -- so only the contents
    // swap. Both shells mount this, so the rule lives in one place.
    import Gallery from './Gallery.svelte';
    import AlbumGrid from './AlbumGrid.svelte';
    import { photosMode } from './gallery-store';
</script>

{#if $photosMode.kind === 'albums'}
    <AlbumGrid />
{:else}
    <Gallery />
{/if}
