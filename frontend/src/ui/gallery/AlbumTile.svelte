<script lang="ts">
    import FolderIcon from '@lucide/svelte/icons/folder';
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

    // A cover that cannot be drawn is a folder glyph: no cover at all, a video
    // (which has no still of its own), a stale revision, or a failed fetch.
    // Never a broken image -- the tile still navigates, and saying "broken"
    // about a folder that is perfectly fine would be a lie about the drive.
    const glyph = $derived(!tile.cover || status === 'failed' || status === 'missing');
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
        {#if glyph}
            <span class="album-cover-glyph"><FolderIcon size={30} strokeWidth={1.5} aria-hidden="true" /></span>
        {:else}
            <img class="gallery-thumb" alt="" width="512" height="512" decoding="async" src={src || undefined} />
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
