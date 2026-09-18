import { writable } from 'svelte/store';
import type { GallerySource } from './gallery-source';
import type { AlbumTile } from './album-view';

export type GalleryView =
    | { status: 'loading' }
    | { status: 'error' }
    | { status: 'empty' }
    | { status: 'ready'; source: GallerySource; initialIndex?: number; anchorOffset?: number };

export const galleryView = writable<GalleryView>({ status: 'loading' });

/**
 * Which photos Photos is showing. All three are the same drive seen three
 * ways, so moving between them re-renders in place rather than navigating:
 * `albums` is the folder grid, `timeline` the whole drive newest-first, and
 * `album` one folder's photos through that same timeline, scoped.
 */
export type PhotosMode =
    | { kind: 'albums' }
    | { kind: 'timeline' }
    | { kind: 'album'; tile: AlbumTile };

export const photosMode = writable<PhotosMode>({ kind: 'timeline' });

/**
 * The album grid's tiles. Names and counts come from the local projection, so
 * the grid stays useful offline and with the vault locked -- only the covers
 * need the network and the key.
 */
export type AlbumsView =
    | { status: 'loading' }
    | { status: 'ready'; tiles: AlbumTile[] };

export const albumsView = writable<AlbumsView>({ status: 'loading' });
