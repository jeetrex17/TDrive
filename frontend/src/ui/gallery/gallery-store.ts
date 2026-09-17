import { writable } from 'svelte/store';
import type { GallerySource } from './gallery-source';

export type GalleryView =
    | { status: 'loading' }
    | { status: 'error' }
    | { status: 'empty' }
    | { status: 'ready'; source: GallerySource; initialIndex?: number; anchorOffset?: number };

export const galleryView = writable<GalleryView>({ status: 'loading' });
