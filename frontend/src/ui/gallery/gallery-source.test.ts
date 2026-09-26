import { describe, expect, it, vi } from 'vitest';
import { GallerySource } from './gallery-source';
import type { GalleryItem, MediaPage, MediaTimeline } from '../../api/gallery';

vi.mock('../../api/gallery', () => ({ listMediaPage: vi.fn() }));

const item = (msgId: number): GalleryItem => ({ msgId, name: `${msgId}.jpg`, size: 1, parentId: '', uploadTime: 1, uploaderId: 0, encrypted: false, plaintextSize: 0, revision: 1, contentMsgId: msgId, contentHash: '' });
const timeline: MediaTimeline = {
    channelId: 1, generation: '1', totalCount: 100_000, pageSize: 128,
    buckets: [{ key: '2026-09', startIndex: 0, count: 100_000, uploadTime: 1 }],
    anchors: Array.from({ length: Math.ceil(100_000 / 128) }, (_, page) => ({ startIndex: page * 128, cursor: String(page * 128) })),
};
const load = async (cursor: string): Promise<MediaPage> => {
    const startIndex = Number(cursor);
    return { generation: '1', startIndex, items: Array.from({ length: Math.min(128, 100_000 - startIndex) }, (_, offset) => item(startIndex + offset + 1)), nextCursor: '' };
};

describe('bounded gallery page source', () => {
    it('loads page zero before anchors and releases a deep request after anchors arrive', async () => {
        const summary = { ...timeline, anchors: [] };
        const loader = vi.fn(load);
        const source = new GallerySource(summary, { load: loader });
        expect((await source.get(0))?.msgId).toBe(1);
        let settled = false;
        const deep = source.get(256).then((value) => { settled = true; return value; });
        await Promise.resolve();
        expect(settled).toBe(false);
        source.installAnchors(timeline);
        expect((await deep)?.msgId).toBe(257);
    });

    it('settles anchor-blocked requests on failure and disposal', async () => {
        const source = new GallerySource({ ...timeline, anchors: [] }, { load });
        const failed = source.get(256);
        source.failAnchors(new Error('anchor scan failed'));
        await expect(failed).rejects.toThrow('anchor scan failed');
        const disposed = new GallerySource({ ...timeline, anchors: [] }, { load });
        const pending = disposed.get(256);
        disposed.dispose();
        await expect(pending).resolves.toBeUndefined();
    });

    it('retries a transient anchor failure on later deep demand', async () => {
        const anchors = vi.fn<() => Promise<MediaTimeline>>()
            .mockRejectedValueOnce(new Error('offline'))
            .mockResolvedValueOnce(timeline);
        const source = new GallerySource({ ...timeline, anchors: [] }, { load, loadAnchors: anchors });
        await expect(source.get(256)).rejects.toThrow('offline');
        expect((await source.get(256))?.msgId).toBe(257);
        expect(anchors).toHaveBeenCalledTimes(2);
    });
    it('does not schedule outside bounds and unregisters notifications cleanly', async () => {
        const loader = vi.fn(load);
        const source = new GallerySource(timeline, { load: loader });
        const notify = vi.fn();
        const unsubscribe = source.subscribe(notify);
        expect(await source.get(-1)).toBeUndefined();
        expect(await source.get(100_000)).toBeUndefined();
        await source.get(0);
        expect(notify).toHaveBeenCalledOnce();
        unsubscribe();
        await source.get(256);
        expect(notify).toHaveBeenCalledOnce();
        expect(source.indexOf(257)).toBe(256);
        expect(source.indexOf(999_999)).toBeUndefined();
        source.dispose();
        source.ensureRange(1000, 1200);
        expect(await source.get(1000)).toBeUndefined();
        expect(loader).toHaveBeenCalledTimes(2);
    });

    it('reports and retries a failed viewport page without replacing the library', async () => {
        const loader = vi.fn(load).mockRejectedValueOnce(new Error('offline'));
        const source = new GallerySource(timeline, { load: loader });
        source.ensureRange(0, 10);
        await vi.waitFor(() => expect(source.error).toContain('Could not load'));
        source.ensureRange(0, 10);
        await source.get(0);
        expect(source.error).toBe('');
        expect(source.peek(0)?.msgId).toBe(1);
    });

    it('rejects an incomplete page and asks for a new snapshot on stale errors', async () => {
        const invalid = new GallerySource(timeline, { load: async () => ({ ...await load('0'), items: [] }) });
        await expect(invalid.get(0)).rejects.toThrow('Invalid gallery page');
        const stale = vi.fn();
        const source = new GallerySource(timeline, { load: async () => { throw new Error('gallery snapshot is stale'); }, onStale: stale });
        source.ensureRange(0, 10);
        await vi.waitFor(() => expect(source.error).toBe('Updating photos…'));
        await expect(source.get(0)).rejects.toThrow('stale');
        expect(stale).toHaveBeenCalledOnce();
    });
    it('uses one bounded scheduler even for rapid explicit keyboard/viewer requests', async () => {
        const resolvers: Array<() => void> = [];
        let active = 0;
        let maximum = 0;
        const source = new GallerySource(timeline, { load: async (cursor) => {
            active += 1;
            maximum = Math.max(maximum, active);
            await new Promise<void>((resolve) => resolvers.push(resolve));
            active -= 1;
            return load(cursor);
        } });
        const pending = Array.from({ length: 100 }, (_, index) => source.get(index * 128));
        expect(active).toBe(2);
        expect(source.pendingCount).toBeLessThanOrEqual(8);
        for (let round = 0; round < 10; round += 1) {
            for (const resolve of resolvers.splice(0)) resolve();
            await Promise.resolve(); await Promise.resolve(); await Promise.resolve();
        }
        await Promise.all(pending);
        expect(maximum).toBe(2);
    });

    it('keeps viewport records available after the viewer traverses many pages', async () => {
        const source = new GallerySource(timeline, { maxPages: 6, load });
        source.ensureRange(0, 100);
        await source.get(0);
        for (let index = 1000; index < 50_000; index += 1000) await source.get(index);
        expect(source.peek(0)?.msgId).toBe(1);
        expect(source.retainedCount).toBeLessThanOrEqual(768);
    });
    it('keeps at most 768 records while visiting a 100,000 photo library', async () => {
        const source = new GallerySource(timeline, { maxPages: 6, load });
        for (let index = 0; index < 100_000; index += 1024) await source.get(index);
        expect(source.retainedCount).toBeLessThanOrEqual(768);
        expect(source.peek(0)).toBeUndefined();
        expect((await source.get(0))?.msgId).toBe(1);
        source.dispose();
        expect(source.retainedCount).toBe(0);
    });

    it('deduplicates overlapping page requests', async () => {
        const loader = vi.fn(load);
        const source = new GallerySource(timeline, { maxPages: 6, load: loader });
        const result = await Promise.all([source.get(20), source.get(21)]);
        expect(loader).toHaveBeenCalledTimes(1);
        expect(result.map((entry) => entry?.msgId)).toEqual([21, 22]);
    });

    it('does not retain a late page after disposal or a different generation', async () => {
        let resolve!: (page: MediaPage) => void;
        const source = new GallerySource(timeline, { load: () => new Promise((done) => { resolve = done; }) });
        const pending = source.get(0);
        await Promise.resolve();
        source.dispose();
        resolve(await load('0'));
        await expect(pending).resolves.toBeUndefined();
        expect(source.retainedCount).toBe(0);

        const stale = vi.fn();
        const changed = new GallerySource(timeline, { load: async () => ({ ...await load('0'), generation: '2' }), onStale: stale });
        await expect(changed.get(0)).rejects.toThrow('gallery snapshot is stale');
        expect(stale).toHaveBeenCalledOnce();
        expect(changed.retainedCount).toBe(0);
    });

    // One folder has no anchor index: the backend hands over the key to the
    // next page with the page before it, which is the only way through.
    it('follows the cursor chain when a scope has no anchor index', async () => {
        const folder: MediaTimeline = {
            channelId: 1, generation: '1', totalCount: 500, pageSize: 128,
            buckets: [{ key: '2026-09', startIndex: 0, count: 500, uploadTime: 1 }],
            anchors: [],
        };
        const chained = vi.fn(async (cursor: string): Promise<MediaPage> => {
            const startIndex = cursor === '' ? 0 : Number(cursor);
            const count = Math.min(128, 500 - startIndex);
            return {
                generation: '1', startIndex,
                items: Array.from({ length: count }, (_, offset) => item(startIndex + offset + 1)),
                nextCursor: startIndex + 128 < 500 ? String(startIndex + 128) : '',
            };
        });
        const source = new GallerySource(folder, { load: chained });
        // A jump past the loaded pages walks the gap once and lands on it.
        expect((await source.get(300))?.msgId).toBe(301);
        expect(chained.mock.calls.map(([cursor]) => cursor)).toEqual(['', '128', '256']);
        // Every cursor learned on the way stays put: a second visit is free.
        chained.mockClear();
        expect((await source.get(260))?.msgId).toBe(261);
        expect(chained).not.toHaveBeenCalled();
    });
});
