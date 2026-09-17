import { afterEach, describe, expect, it } from 'vitest';
import { render } from 'svelte/server';
import Gallery from './Gallery.svelte';
import GalleryCell from './GalleryCell.svelte';
import { galleryView } from './gallery-store';
import { GallerySource } from './gallery-source';
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
        revision: 1,
        ...overrides,
    };
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

    it('renders loaded month groups without retaining offscreen containers', async () => {
        const source = new GallerySource({
            channelId: 1, generation: '1', totalCount: 3, pageSize: 128,
            buckets: [{ key: '2026-07', startIndex: 0, count: 2, uploadTime: 1 }, { key: '2026-06', startIndex: 2, count: 1, uploadTime: 1 }],
            anchors: [{ startIndex: 0, cursor: 'first' }],
        }, { load: async () => ({ generation: '1', startIndex: 0, nextCursor: '', items: [10, 11, 12].map((msgId) => ({ ...makeItem({ msgId }), revision: 1, contentMsgId: msgId, contentHash: '' })) }) });
        await source.get(0);
        galleryView.set({ status: 'ready', source });

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
