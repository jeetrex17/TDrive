import { describe, expect, it, vi } from 'vitest';
vi.mock('../../bindings/TDrive/app', () => ({ GetMediaTimeline: vi.fn(), GetMediaTimelineSummary: vi.fn(), GetMediaTimelineAnchors: vi.fn(), GetMediaFolderTimeline: vi.fn(), ListMediaPage: vi.fn(), ListMediaFolderPage: vi.fn(), ListMediaFolders: vi.fn(), LocateMedia: vi.fn() }));
import { normalizeMediaFolders, normalizeMediaPage, normalizeMediaTimeline } from './gallery';

const timeline = { channel_id: 1, generation: 'g', total_count: 2, page_size: 128, buckets: [{ key: '2026-09', start_index: 0, count: 2, upload_time: 1 }], anchors: [{ start_index: 0, cursor: 'opaque' }] };

describe('gallery API boundary', () => {
    it('keeps content identity while normalizing page records', () => {
        const page = normalizeMediaPage({ generation: 'g', start_index: 0, next_cursor: '', items: [{ msg_id: 1, name: 'photo.jpg', size: 12, upload_time: 1, uploader_id: 0, encrypted: true, revision: 3, content_msg_id: 25, content_hash: 'hash' }] });
        expect(page.items[0]).toMatchObject({ msgId: 1, revision: 3, contentMsgId: 25, contentHash: 'hash', encrypted: true });
        expect(normalizeMediaTimeline(timeline)).toMatchObject({ totalCount: 2, pageSize: 128, anchors: [{ startIndex: 0, cursor: 'opaque' }] });
    });

    it('rejects invalid sizes, missing anchors, and discontinuous buckets', () => {
        expect(() => normalizeMediaTimeline({ ...timeline, page_size: 100_000 })).toThrow();
        expect(() => normalizeMediaTimeline({ ...timeline, anchors: [] })).toThrow();
        expect(() => normalizeMediaTimeline({ ...timeline, buckets: [{ ...timeline.buckets[0], start_index: 1 }] })).toThrow();
        expect(() => normalizeMediaPage({ items: [{ msg_id: -1 }] })).toThrow();
        expect(() => normalizeMediaPage({ items: new Array(100_000) })).toThrow();
    });

    it('accepts an empty library without creating placeholder records', () => {
        expect(normalizeMediaTimeline({ ...timeline, total_count: 0, buckets: [], anchors: [] }).totalCount).toBe(0);
    });

    it('accepts an anchor-free summary but still requires complete full timelines', () => {
        expect(normalizeMediaTimeline({ ...timeline, anchors: [] }, true).anchors).toEqual([]);
        expect(() => normalizeMediaTimeline({ ...timeline, total_count: 300, anchors: timeline.anchors })).toThrow();
    });
});

describe('album folder boundary', () => {
    const camera = { folder_id: 'd:camera', name: 'Camera', item_count: 595, latest_upload_time: 9, cover_msg_id: 5, cover_revision: 2, cover_name: 'IMG_5.jpg' };

    it('keeps the backend order and bounds every string it takes', () => {
        const folders = normalizeMediaFolders([camera, { ...camera, folder_id: '', name: 'x'.repeat(4096) }]);
        expect(folders.map((folder) => folder.folderId)).toEqual(['d:camera', '']);
        expect(folders[0]).toMatchObject({ name: 'Camera', itemCount: 595, coverMsgId: 5, coverRevision: 2 });
        expect(folders[1].name).toHaveLength(512);
    });

    it('drops a row it cannot make sense of instead of the whole grid', () => {
        expect(normalizeMediaFolders([{ ...camera, item_count: -1 }, camera])).toHaveLength(1);
        expect(normalizeMediaFolders(null)).toEqual([]);
    });
});
