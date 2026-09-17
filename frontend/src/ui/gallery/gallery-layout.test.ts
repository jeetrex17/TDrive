import { describe, expect, it } from 'vitest';
import { createGalleryLayout, galleryWindow, offsetForIndex, indexAtOffset } from './gallery-layout';

describe('gallery geometry', () => {
    const buckets = [
        { key: '2026-09', startIndex: 0, count: 70_000, uploadTime: 1 },
        { key: '2026-08', startIndex: 70_000, count: 30_000, uploadTime: 1 },
    ];

    it('keeps the visible row count bounded for a 100,000 photo library', () => {
        const layout = createGalleryLayout(buckets, 390, true);
        for (const top of [0, 500_000, layout.height - 800]) {
            const visible = galleryWindow(layout, top, 800);
            expect(visible.rows.length).toBeLessThan(18);
            expect(visible.rows.flatMap((row) => row.indices).length).toBeLessThan(54);
        }
    });

    it('preserves an item anchor across width and column changes', () => {
        const portrait = createGalleryLayout(buckets, 390, true);
        const landscape = createGalleryLayout(buckets, 800, true);
        const index = 74_997;
        const nextOffset = offsetForIndex(landscape, index);
        const nextIndex = indexAtOffset(landscape, nextOffset);
        expect(index - nextIndex).toBeLessThan(landscape.columns);
        expect(index - indexAtOffset(portrait, offsetForIndex(portrait, index))).toBeLessThan(portrait.columns);
    });

    it('never merges rows across a month boundary or renders invalid indices', () => {
        const layout = createGalleryLayout([
            { key: 'a', startIndex: 0, count: 2, uploadTime: 1 },
            { key: 'b', startIndex: 2, count: 4, uploadTime: 1 },
        ], 390, true);
        const visible = galleryWindow(layout, 0, 800);
        expect(visible.rows.map((row) => row.indices)).toEqual([[0, 1], [2, 3, 4], [5]]);
        expect(visible.headers).toHaveLength(2);
        expect(galleryWindow(createGalleryLayout([], 0, false), 0, 800).rows).toEqual([]);
    });
});
