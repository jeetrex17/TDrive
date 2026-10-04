import { describe, expect, it } from 'vitest';
import { createChannelMediaPager, type ChannelMediaRequest, type ChannelSource } from './channel-model';

const SOURCE: ChannelSource = { channelId: 44, title: 'Cinema', username: 'cinema', connected: true, protected: false, accountId: 'account-a', generation: 'one' };
const item = (msgId: number) => ({ msgId, date: 1, name: `clip-${msgId}.mp4`, size: 10, mimeType: 'video/mp4', kind: 'video', caption: '', streamable: true, blockReason: '', telegramUrl: '' });

describe('channel media pager', () => {
    it('drops a response when the source changes while it is loading', async () => {
        let resolveFirst!: (value: ReturnType<typeof page>) => void;
        const pager = createChannelMediaPager((_request) => new Promise((done) => { resolveFirst = done; }));
        pager.select(SOURCE);
        const pending = pager.load();
        pager.select({ ...SOURCE, channelId: 45, generation: 'two' });
        resolveFirst(page(44, [item(1)]));
        await pending;
        expect(pager.snapshot()).toEqual({ kind: 'idle' });
    });

    it('resets cursor and records for a new search before requesting again', async () => {
        const calls: ChannelMediaRequest[] = [];
        const pager = createChannelMediaPager(async (request) => {
            calls.push(request);
            return page(44, [item(request.offsetId + 1)], request.offsetId === 0 ? 9 : 0);
        });
        pager.select(SOURCE);
        await pager.load();
        await pager.load({ append: true });
        pager.setFilter('live set', 'audio');
        await pager.load();
        expect(calls.map((call) => [call.offsetId, call.search, call.kind])).toEqual([
            [0, '', 'all'], [9, '', 'all'], [0, 'live set', 'audio'],
        ]);
    });

    it('keeps only the bounded most recent record window', async () => {
        const pager = createChannelMediaPager(async (request) => page(44, Array.from({ length: 40 }, (_, index) => item(request.offsetId + index + 1)), request.offsetId < 200 ? request.offsetId + 40 : 0));
        pager.select(SOURCE);
        await pager.load();
        for (let index = 0; index < 5; index += 1) await pager.load({ append: true });
        const state = pager.snapshot();
        expect(state.kind).toBe('ready');
        if (state.kind === 'ready') {
            expect(state.items).toHaveLength(200);
            expect(state.items[0].msgId).toBe(41);
        }
    });

    it('keeps a UTF-8 search inside the backend byte limit', async () => {
        const calls: ChannelMediaRequest[] = [];
        const pager = createChannelMediaPager(async (request) => { calls.push(request); return page(44, []); });
        pager.select(SOURCE);
        pager.setFilter('🎬'.repeat(80), 'all');
        await pager.load();
        expect(new TextEncoder().encode(calls[0].search).length).toBeLessThanOrEqual(120);
    });
});

function page(channelId: number, items: ReturnType<typeof item>[], nextOffsetId = 0) {
    return { channelId, accountId: 'account-a', generation: 'one', items, nextOffsetId, hasMore: nextOffsetId > 0 };
}
