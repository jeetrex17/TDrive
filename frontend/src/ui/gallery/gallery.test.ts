import { afterEach, describe, expect, it } from 'vitest';
import { render } from 'svelte/server';
import Gallery from './Gallery.svelte';
import GalleryCell from './GalleryCell.svelte';
import { galleryView, type GalleryGroup } from './gallery-store';
import type { FileItem } from '../../types';

function makeItem(overrides: Partial<FileItem> = {}): FileItem {
    return {
        msgId: 1,
        name: 'photo.jpg',
        size: 1000,
        parentId: '',
        uploadTime: 0,
        uploaderId: 0,
        encrypted: false,
        plaintextSize: 0,
        ...overrides,
    };
}

function makeGroup(label: string, items: FileItem[], startIndex = 0): GalleryGroup {
    return { label, cells: items.map((item, i) => ({ item, index: startIndex + i })) };
}

afterEach(() => {
    galleryView.set({ status: 'loading' });
});

describe('Gallery view states', () => {
    it('renders the loading placeholder', () => {
        galleryView.set({ status: 'loading' });
        expect(render(Gallery).body).toContain('Loading photos…');
    });

    it('renders the error and empty states', () => {
        galleryView.set({ status: 'error' });
        expect(render(Gallery).body).toContain('Could not load photos.');

        galleryView.set({ status: 'empty' });
        const empty = render(Gallery).body;
        expect(empty).toContain('gallery-empty');
        expect(empty).toContain('No photos yet');
    });

    it('renders month groups with a cell per item', () => {
        galleryView.set({
            status: 'ready',
            groups: [
                makeGroup('July 2026', [makeItem({ msgId: 10 }), makeItem({ msgId: 11 })], 0),
                makeGroup('June 2026', [makeItem({ msgId: 12 })], 2),
            ],
        });

        const { body } = render(Gallery);

        expect(body).toContain('July 2026');
        expect(body).toContain('June 2026');
        expect((body.match(/gallery-cell/g) || [])).toHaveLength(3);
        expect(body).toContain('data-id="10"');
        expect(body).toContain('data-index="2"');
    });
});

describe('GalleryCell', () => {
    it('starts idle with no image source and a plain title', () => {
        const { body } = render(GalleryCell, { props: { item: makeItem({ name: 'a.jpg' }), index: 0 } });

        expect(body).toContain('class="gallery-cell"');
        expect(body).not.toContain('is-loaded');
        expect(body).not.toContain('src=');
        expect(body).toContain('title="a.jpg"');
        expect(body).not.toContain('gallery-lock');
    });

    it('shows a lock badge for encrypted items', () => {
        const { body } = render(GalleryCell, { props: { item: makeItem({ encrypted: true }), index: 0 } });
        expect(body).toContain('gallery-lock');
    });

    it('escapes untrusted names in the aria-label and title', () => {
        const { body } = render(GalleryCell, { props: { item: makeItem({ name: '<img src=x>.jpg' }), index: 0 } });
        expect(body).not.toContain('<img src=x>');
    });
});
