<script lang="ts">
    import FilmIcon from '@lucide/svelte/icons/film';
    import ImagesIcon from '@lucide/svelte/icons/images';
    import LockKeyholeIcon from '@lucide/svelte/icons/lock-keyhole';
    import type { FileThumbnailIdentity } from '../file-list/types';
    import { registerCell, unregisterCell, type CellPatch, type CellStatus } from './gallery-controller';
    import type { AlbumTile } from './album-view';

    interface Props {
        tile: AlbumTile;
        index: number;
        tabindex: number;
        onOpen: (tile: AlbumTile) => void;
    }

    let { tile, index, tabindex, onOpen }: Props = $props();

    let status = $state<CellStatus>('idle');
    let src = $state('');
    let detail = $state('');

    // While a cover is on its way the tile is a quiet surface (a shimmer once
    // the fetch starts), so a cold grid does not flash a glyph per tile. When
    // there is no picture to show -- none renderable, a stale revision, a
    // failed fetch -- the tile names what the album holds instead: photos, or
    // a film for a video cover. Never a broken image, and never a folder: this
    // is an album of pictures, not a place in the file tree. A locked vault
    // draws its own lock, so the two never stack.
    const glyph = $derived(tile.cover === undefined || status === 'failed' || status === 'missing');
    const image = $derived(tile.cover !== undefined && status !== 'failed' && status !== 'missing');
    const coverClass = $derived(
        `album-cover${status === 'loaded' ? ' is-loaded' : ''}${status === 'loading' ? ' is-loading' : ''}${status === 'locked' ? ' is-locked' : ''}`,
    );
    // The detail carries the locked/unavailable wording the broker reports, in
    // the same words the file list and the photo grid use for it.
    const title = $derived(detail ? `${tile.label} — ${detail}` : tile.label);

    function apply(patch: CellPatch): void {
        if (patch.status !== undefined) status = patch.status;
        if (patch.src !== undefined) src = patch.src;
        if (patch.title !== undefined) detail = patch.title;
    }

    // The cover leases through the shared rendition broker, exactly as a file
    // row or a photo cell does: one registration per mounted tile, a lease only
    // while it intersects, and eviction, byte caps and cancellation already
    // owned elsewhere. A folder with no renderable cover registers nothing.
    function cover(node: HTMLElement, identity: FileThumbnailIdentity | undefined) {
        if (identity) registerCell(node, { msgId: identity.fileId, revision: identity.revision, apply });
        return {
            update(next: FileThumbnailIdentity | undefined) {
                if (next?.fileId === identity?.fileId && next?.revision === identity?.revision) return;
                identity = next;
                status = 'idle';
                src = '';
                detail = '';
                unregisterCell(node);
                if (identity) registerCell(node, { msgId: identity.fileId, revision: identity.revision, apply });
            },
            destroy() {
                unregisterCell(node);
            },
        };
    }
</script>

<button
    class="album-tile"
    type="button"
    data-index={index}
    data-folder-id={tile.folderId}
    {tabindex}
    {title}
    aria-label={tile.label}
    onclick={() => onOpen(tile)}
>
    <span class={coverClass} use:cover={tile.cover}>
        {#if image}
            <img class="gallery-thumb" alt="" width="512" height="512" decoding="async" src={src || undefined} />
        {/if}
        {#if glyph}
            <span class="album-cover-glyph is-empty">
                {#if tile.coverIsVideo}<FilmIcon size={26} strokeWidth={1.5} aria-hidden="true" />{:else}<ImagesIcon size={26} strokeWidth={1.5} aria-hidden="true" />{/if}
            </span>
        {/if}
        {#if status === 'locked'}
            <span class="album-cover-glyph"><LockKeyholeIcon size={26} strokeWidth={1.6} aria-hidden="true" /></span>
        {/if}
    </span>
    <span class="album-label">
        <span class="album-name">{tile.name}</span>
        <span class="album-count">{tile.countLabel}</span>
    </span>
</button>
