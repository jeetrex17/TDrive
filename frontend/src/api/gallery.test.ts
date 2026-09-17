import { describe, expect, it, vi } from 'vitest';
vi.mock('../../bindings/TDrive/app', () => ({ GetMediaTimeline: vi.fn(), ListMediaPage: vi.fn(), LocateMedia: vi.fn() }));
import { normalizeMediaPage, normalizeMediaTimeline } from './gallery';

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
});
