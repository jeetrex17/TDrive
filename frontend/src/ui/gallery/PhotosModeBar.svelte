<script lang="ts">
    // What Photos is showing, and how to change it. Two toggle buttons rather
    // than a tablist: the grid and the timeline are not labelled tab panels,
    // they are one region re-rendered, and a pressed pair says exactly that
    // with the semantics the rest of the app already uses for a segment.
    import ChevronLeftIcon from '@lucide/svelte/icons/chevron-left';
    import { showPhotos } from '../../modules/gallery';
    import { albumsWorthShowing } from './album-view';
    import { albumsView, photosMode } from './gallery-store';

    const tiles = $derived($albumsView.status === 'ready' ? $albumsView.tiles : []);
    // With no structure to show, the switch would only offer the view that is
    // already on screen. A dead control invites a click that does nothing.
    const switchable = $derived(albumsWorthShowing(tiles));
    const mode = $derived($photosMode);
</script>

<div class="photos-mode-bar">
    {#if mode.kind === 'album'}
        <button class="album-back" type="button" onclick={() => void showPhotos({ kind: 'albums' })}>
            <ChevronLeftIcon size={16} strokeWidth={2.2} aria-hidden="true" />
            <span class="album-back-name">{mode.tile.name}</span>
        </button>
        <span class="album-back-count">{mode.tile.countLabel}</span>
    {:else if switchable}
        <div class="photos-modes" role="group" aria-label="Photos view">
            <button
                type="button"
                aria-pressed={mode.kind === 'albums'}
                onclick={() => void showPhotos({ kind: 'albums' })}
            >Albums</button>
            <button
                type="button"
                aria-pressed={mode.kind === 'timeline'}
                onclick={() => void showPhotos({ kind: 'timeline' })}
            >All photos</button>
        </div>
    {/if}
</div>
