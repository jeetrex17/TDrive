import { describe, expect, it, vi } from 'vitest';
import { createChannelTopicPager, MAX_CHANNEL_TOPICS, type ChannelTopicPage } from './channel-topics';
import type { MediaOpenResult } from '../../api';
import {
    boundedSearch,
    channelVideoQueue,
    createChannelMediaPager,
    MAX_CHANNEL_ITEMS,
    mediaMeta,
    isOpenable,
    mediaActionLabel,
    mediaTitle,
    restrictionLabel,
    sourceKey,
    sourcePeerLabel,
    sortPosts,
    type ChannelMediaItem,
    type ChannelMediaPage,
    type ChannelMediaRequest,
    type ChannelSource,
} from './channel-model';

const SOURCE: ChannelSource = { peerKind: 'channel', peerId: 44, title: 'Cinema', username: 'cinema', connected: true, protected: false, available: true, accountId: '7', generation: 'one' };

function post(msgId: number, extra: Partial<ChannelMediaItem> = {}): ChannelMediaItem {
    return {
        msgId, date: 0, name: `clip-${msgId}.mp4`, size: 0, duration: 0, mimeType: 'video/mp4', kind: 'video',
        caption: '', streamable: true, blockReason: '', telegramUrl: '', ...extra,
    };
}

function page(items: ChannelMediaItem[], nextOffsetId = 0, extra: Partial<ChannelMediaPage> = {}): ChannelMediaPage {
    return { peerKind: 'channel', peerId: 44, accountId: '7', generation: 'one', items, nextOffsetId, hasMore: nextOffsetId > 0, ...extra };
}

describe('channel media pager', () => {
    it('keeps sources distinct when peer ids overlap across types or accounts', () => {
        expect(sourceKey(SOURCE)).toBe('7:channel:44:one');
        expect(sourceKey({ ...SOURCE, peerKind: 'user' })).toBe('7:user:44:one');
        expect(sourceKey({ ...SOURCE, accountId: '8' })).toBe('8:channel:44:one');
        expect(sourcePeerLabel({ peerKind: 'self' })).toBe('Saved Messages');
    });

    it('drops an answer for a channel that is no longer selected', async () => {
        let answer!: (value: ChannelMediaPage) => void;
        const pager = createChannelMediaPager(() => new Promise((resolve) => { answer = resolve; }));
        pager.select(SOURCE);
        const pending = pager.load();
        pager.select({ ...SOURCE, peerId: 45, generation: 'two' });
        answer(page([post(1)]));
        await pending;
        expect(pager.snapshot()).toEqual({ status: 'idle' });
    });

    it('starts again from the newest post when the filter changes', async () => {
        const calls: ChannelMediaRequest[] = [];
        const pager = createChannelMediaPager(async (request) => {
            calls.push(request);
            return page([post(request.offsetId + 1)], request.offsetId === 0 ? 9 : 0);
        });
        pager.select(SOURCE);
        await pager.load();
        await pager.load({ append: true });
        pager.setFilter('  live set ', 'audio');
        await pager.load();
        expect(calls.map((call) => [call.offsetId, call.search, call.kind])).toEqual([
            [0, '', 'all'], [9, '', 'all'], [0, 'live set', 'audio'],
        ]);
    });

    it('separates General from All and rejects stale or mis-scoped topic pages', async () => {
        let answer!: (value: ChannelMediaPage) => void;
        const fetch = vi.fn(() => new Promise<ChannelMediaPage>((resolve) => { answer = resolve; }));
        const pager = createChannelMediaPager(fetch);
        pager.select(SOURCE);
        const old = pager.load();
        pager.setTopic(1);
        answer(page([post(9)]));
        await old;
        expect(pager.snapshot()).toEqual({ status: 'idle' });
        const general = pager.load();
        expect(fetch).toHaveBeenLastCalledWith(expect.objectContaining({ topicId: 1, offsetId: 0 }));
        answer(page([post(8, { topicId: 42 })], 0, { topicId: 1 }));
        expect((await general).status).toBe('error');
        const retry = pager.load();
        answer(page([post(7, { topicId: 1 })], 0, { topicId: 1, forum: true }));
        expect(await retry).toMatchObject({ status: 'ready', items: [post(7, { topicId: 1 })] });
        pager.setTopic(0);
        const all = pager.load();
        answer(page([post(8, { topicId: 42 })], 0, { topicId: 0, forum: true }));
        expect(await all).toMatchObject({ status: 'ready', items: [post(8, { topicId: 42 })] });
    });

    it('stops paging at the item budget instead of dropping posts already shown', async () => {
        const pager = createChannelMediaPager(async (request) => page(
            Array.from({ length: 40 }, (_, index) => post(100_000 - request.offsetId - index)),
            request.offsetId + 40,
        ));
        pager.select(SOURCE);
        await pager.load();
        for (let index = 0; index < 12; index += 1) await pager.load({ append: true });
        const view = pager.snapshot();
        expect(view).toMatchObject({ status: 'ready', hasMore: false, capped: true });
        if (view.status !== 'ready') return;
        expect(view.items).toHaveLength(MAX_CHANNEL_ITEMS);
        expect(view.items[0].msgId).toBe(100_000);
    });

    it('shows an error rather than a spinner when a page belongs to another connection', async () => {
        const pager = createChannelMediaPager(async () => page([post(1)], 0, { generation: 'replaced' }));
        pager.select(SOURCE);
        const view = await pager.load();
        expect(view.status).toBe('error');
    });
});

describe('channel video queue', () => {
    it("queues the list's playable videos in order, each opening through its own post", async () => {
        const opened: number[] = [];
        const posts = [
            post(9, { caption: 'Dawn chorus' }),
            post(8, { kind: 'audio', name: 'storm.mp3' }),
            post(7, { streamable: false, blockReason: 'paid' }),
            post(6, { name: 'Telegram media 6.mp4', caption: 'Coastline' }),
        ];
        const { target, playlist } = channelVideoQueue(posts[3], SOURCE, posts, async (item) => {
            opened.push(item.msgId);
            return {} as MediaOpenResult;
        });
        expect(playlist.title).toBe('Videos in Cinema');
        expect(playlist.items.map((item) => [item.key, item.title])).toEqual([
            ['source:7:channel:44:one:9', 'Dawn chorus'],
            ['source:7:channel:44:one:6', 'Coastline'],
        ]);
        expect(playlist.currentIndex).toBe(1);
        expect(target).toMatchObject({ id: 6, name: 'Telegram media 6.mp4', title: 'Coastline' });
        await playlist.items[0].open?.();
        await target.open?.();
        expect(opened).toEqual([9, 6]);
    });
});

describe('sorting posts', () => {
    const posts = [
        post(30, { caption: 'Episode 10', date: 300, size: 5, duration: 40 }),
        post(20, { caption: 'Episode 2', date: 200, size: 9, duration: 0 }),
        post(10, { caption: 'episode 1', date: 100, size: 7, duration: 90 }),
    ];
    const order = (sort: Parameters<typeof sortPosts>[1]) => sortPosts(posts, sort).map((item) => item.msgId);

    it('keeps Telegram newest-first order for Newest, and reverses it for Oldest', () => {
        expect(sortPosts(posts, 'newest')).toBe(posts);
        expect(order('oldest')).toEqual([10, 20, 30]);
    });

    it('orders names the way people read them, episode 2 before episode 10', () => {
        expect(order('name')).toEqual([10, 20, 30]);
    });

    it('puts the largest and the longest first', () => {
        expect(order('size')).toEqual([20, 10, 30]);
        expect(order('length')).toEqual([10, 30, 20]);
    });
});

describe('boundedSearch', () => {
    it('keeps a query within the 120-byte backend limit without splitting a character', () => {
        expect(new TextEncoder().encode(boundedSearch('🎬'.repeat(80)))).toHaveLength(120);
        expect(boundedSearch(`${'a'.repeat(119)}é`)).toBe('a'.repeat(119));
    });
});

describe('post presentation', () => {
    it('names a video by its caption and an audio post by its file name', () => {
        expect(mediaTitle(post(1, { caption: '\nDawn chorus\nrecorded at 4:50' }))).toBe('Dawn chorus');
        expect(mediaTitle(post(2, { kind: 'audio', name: 'night-insects.flac', caption: 'Part 3' }))).toBe('night-insects.flac');
    });

    it('does not show the name the backend invents for a file without one', () => {
        expect(mediaTitle(post(3, { name: 'Telegram media 884.mkv' }))).toBe('Untitled video');
        expect(mediaTitle(post(4, { kind: 'audio', name: 'Telegram media' }))).toBe('Untitled audio');
    });

    it('says why Telegram keeps a post, and that an unsupported file is not locked', () => {
        expect(['protected', 'paid', 'expires', 'restricted', 'unsupported_format', ''].map((code) => restrictionLabel(post(5, { blockReason: code }))))
            .toEqual(['Protected', 'Paid', 'Expiring', 'Restricted', '', '']);
        expect(mediaMeta(post(6, { kind: 'unknown', name: 'kit.pdf', size: 1_048_576, date: 0 }))).toBe('PDF · 1 MB');
    });

    it('admits the supported viewer kinds and keeps restrictions and unknown formats outside TDrive', () => {
        for (const kind of ['video', 'audio', 'image', 'pdf', 'text']) {
            const item = post(9, { kind });
            expect(isOpenable(item)).toBe(true);
            expect(mediaActionLabel(item)).toBe(kind === 'video' || kind === 'audio' ? 'Play' : 'Open');
            for (const blockReason of ['protected', 'paid', 'expires', 'restricted', 'unsupported_format']) {
                expect(isOpenable({ ...item, blockReason })).toBe(false);
            }
            expect(isOpenable({ ...item, streamable: false })).toBe(false);
        }
        expect(isOpenable(post(10, { kind: 'unknown' }))).toBe(false);
        expect(mediaMeta(post(11, { kind: 'image', name: 'Attachment' }))).toBe('Image');
        expect(mediaMeta(post(12, { kind: 'pdf', name: 'Attachment' }))).toBe('PDF');
    });

    it('gives a post its running time when Telegram knows it', () => {
        expect(mediaMeta(post(7, { duration: 3_729, size: 0 }))).toBe('Video · 1:02:09');
        expect(mediaMeta(post(8, { kind: 'audio', name: 'a.mp3', duration: 245 }))).toBe('Audio · 4:05');
    });
});

describe('channel topic paging', () => {
    const topic = (id: number) => ({ id, title: `Topic ${id}`, iconColor: 0, iconEmojiId: '', closed: false, hidden: false });
    const topicPage = (extra: Partial<ChannelTopicPage> = {}): ChannelTopicPage => ({
        peerKind: SOURCE.peerKind, peerId: SOURCE.peerId, accountId: SOURCE.accountId, generation: SOURCE.generation,
        forum: true, topics: [topic(1)], next: { offsetDate: 0, offsetId: 1, offsetTopic: 1 }, hasMore: true, ...extra,
    });

    it('drops a previous search and a response from another account', async () => {
        let answer!: (page: ChannelTopicPage) => void;
        const pager = createChannelTopicPager(() => new Promise((resolve) => { answer = resolve; }));
        pager.select(SOURCE);
        const old = pager.load();
        pager.search('Music');
        answer(topicPage());
        await old;
        expect(pager.snapshot().topics).toEqual([]);
        const next = pager.load();
        answer(topicPage({ accountId: '8' }));
        expect(await next).toMatchObject({ status: 'error', topics: [] });
    });

    it('keeps loaded topics and retries the failed cursor before advancing', async () => {
        const fetch = vi.fn().mockResolvedValueOnce(topicPage()).mockRejectedValueOnce(new Error('offline'))
            .mockResolvedValueOnce(topicPage({ topics: [topic(2)], next: { offsetDate: 0, offsetId: 2, offsetTopic: 2 } }));
        const pager = createChannelTopicPager(fetch);
        pager.select(SOURCE);
        await pager.load();
        expect(await pager.load(true)).toMatchObject({ status: 'error', topics: [topic(1)], hasMore: true });
        expect(await pager.load(true)).toMatchObject({ status: 'ready', topics: [topic(1), topic(2)] });
        expect(fetch.mock.calls.slice(1).map(([request]) => request.offsetTopic)).toEqual([1, 1]);
    });

    it('bounds topic records and stops repeated cursors', async () => {
        const fetch = vi.fn(async (request) => topicPage({ topics: Array.from({ length: 40 }, (_, i) => topic(request.offsetTopic + i + 1)), next: { offsetDate: 0, offsetId: 0, offsetTopic: request.offsetTopic + 40 } }));
        const pager = createChannelTopicPager(fetch);
        pager.select(SOURCE);
        await pager.load();
        for (let i = 0; i < 6; i += 1) await pager.load(true);
        expect(pager.snapshot()).toMatchObject({ capped: true, hasMore: false });
        expect(pager.snapshot().topics).toHaveLength(MAX_CHANNEL_TOPICS);
        expect(fetch).toHaveBeenCalledTimes(5);
        const repeated = createChannelTopicPager(async () => topicPage({ next: { offsetDate: 0, offsetId: 0, offsetTopic: 0 } }));
        repeated.select(SOURCE);
        await repeated.load();
        expect(repeated.snapshot().hasMore).toBe(false);
    });
});
