import { writable } from 'svelte/store';
import type { FileItem } from '../../types';

export interface GalleryCellModel {
    item: FileItem;
    // Flat index across all groups, for lightbox prev/next over the whole set.
    index: number;
}

export interface GalleryGroup {
    label: string;
    cells: GalleryCellModel[];
}

export type GalleryView =
    | { status: 'loading' }
    | { status: 'error' }
    | { status: 'empty' }
    | { status: 'ready'; groups: GalleryGroup[] };

export const galleryView = writable<GalleryView>({ status: 'loading' });
