import { boundedSearch, MAX_CHANNEL_ITEMS, sourceKey, type ChannelSource } from './channel-model';

export interface ChannelTopic {
    id: number;
    title: string;
    iconColor: number;
    iconEmojiId: string;
    closed: boolean;
    hidden: boolean;
}

export interface ChannelTopicCursor {
    offsetDate: number;
    offsetId: number;
    offsetTopic: number;
}

export interface ChannelTopicRequest extends ChannelTopicCursor {
    source: ChannelSource;
    limit: number;
    query: string;
}

export interface ChannelTopicPage {
    peerKind: ChannelSource['peerKind'];
    peerId: number;
    accountId: string;
    generation: string;
    forum: boolean;
    topics: readonly ChannelTopic[];
    next: ChannelTopicCursor;
    hasMore: boolean;
}

export type ChannelTopicFetcher = (request: ChannelTopicRequest) => Promise<ChannelTopicPage>;

export interface ChannelTopicView {
    status: 'idle' | 'loading' | 'ready' | 'error';
    topics: readonly ChannelTopic[];
    hasMore: boolean;
    capped: boolean;
    error?: unknown;
}

export const MAX_CHANNEL_TOPICS = 200;
const START: ChannelTopicCursor = { offsetDate: 0, offsetId: 0, offsetTopic: 0 };

/** One paged search, independent of file paging. Late bridge results never change its scope. */
export function createChannelTopicPager(fetch: ChannelTopicFetcher) {
    let source: ChannelSource | null = null;
    let query = '';
    let cursor = START;
    let version = 0;
    let view: ChannelTopicView = { status: 'idle', topics: [], hasMore: false, capped: false };

    function reset(): void {
        version += 1;
        cursor = START;
        view = { status: 'idle', topics: [], hasMore: false, capped: false };
    }

    async function load(append = false): Promise<ChannelTopicView> {
        if (!source || view.status === 'loading' || (append && !view.hasMore)) return view;
        const current = ++version;
        const target = source;
        const previousView = view;
        const previous = append ? view.topics : [];
        const offset = append ? cursor : START;
        view = { status: 'loading', topics: previous, hasMore: false, capped: false };
        try {
            const page = await fetch({ source: target, ...offset, limit: 40, query });
            if (current !== version) return view;
            if (sourceKey(page) !== sourceKey(target)) throw new Error('This group changed while topics were loading. Try again.');
            if (!page.forum) throw Object.assign(new Error('Topics are no longer enabled in this group.'), { code: 'forum_unavailable' });
            const merged = new Map(previous.map((topic) => [topic.id, topic]));
            for (const topic of page.topics) if (topic.id > 0) merged.set(topic.id, topic);
            const topics = [...merged.values()].slice(0, MAX_CHANNEL_TOPICS);
            const advanced = page.next.offsetDate !== offset.offsetDate || page.next.offsetId !== offset.offsetId || page.next.offsetTopic !== offset.offsetTopic;
            cursor = page.next;
            const capped = topics.length >= MAX_CHANNEL_TOPICS;
            view = { status: 'ready', topics, hasMore: page.hasMore && advanced && !capped, capped };
        } catch (error) {
            if (current === version) view = { status: 'error', topics: previous, hasMore: append && previousView.hasMore, capped: previousView.capped, error };
        }
        return view;
    }

    return {
        load,
        snapshot: () => view,
        select(next: ChannelSource | null): void {
            source = next;
            query = '';
            reset();
        },
        search(next: string): void {
            query = boundedSearch(next);
            reset();
        },
        invalidate(): void {
            source = null;
            reset();
        },
    };
}

/** Batched row labels have their own fixed budget, independent of picker search results. */
export function mergeTopicMetadata(previous: ReadonlyMap<number, ChannelTopic>, topics: readonly ChannelTopic[]): ReadonlyMap<number, ChannelTopic> {
    const next = new Map(previous);
    for (const topic of topics) {
        next.delete(topic.id);
        next.set(topic.id, topic);
    }
    while (next.size > MAX_CHANNEL_ITEMS) {
        const oldest = next.keys().next().value;
        if (oldest === undefined) break;
        next.delete(oldest);
    }
    return next;
}
