import type { MediaOpenResult } from '../../api';
import type { VideoOpenTarget, VideoPlaylistLaunch } from '../../modules/video/video-queue';
import { formatBytes, formatDuration, splitNameAndExt } from '../../utils';
import { relativeTimeLabel } from '../file-list/row-meta';

export type ChannelMediaKind = 'all' | 'video' | 'audio';

export interface ChannelSource {
    channelId: number;
    title: string;
    username: string;
    connected: boolean;
    protected: boolean;
    accountId: string;
    generation: string;
}

export interface ChannelMediaItem {
    msgId: number;
    date: number;
    name: string;
    size: number;
    /** Whole seconds, 0 when Telegram did not say. */
    duration: number;
    mimeType: string;
    kind: string;
    caption: string;
    streamable: boolean;
    blockReason: string;
    telegramUrl: string;
}

export interface ChannelMediaPage {
    channelId: number;
    accountId: string;
    generation: string;
    items: readonly ChannelMediaItem[];
    nextOffsetId: number;
    hasMore: boolean;
}

export interface ChannelMediaRequest {
    channelId: number;
    offsetId: number;
    limit: number;
    search: string;
    kind: ChannelMediaKind;
}

export type ChannelMediaFetcher = (request: ChannelMediaRequest) => Promise<ChannelMediaPage>;

export type ChannelMediaView =
    | { status: 'idle' }
    | { status: 'loading'; items: readonly ChannelMediaItem[] }
    | { status: 'ready'; items: readonly ChannelMediaItem[]; hasMore: boolean; capped: boolean }
    | { status: 'empty'; hasMore: boolean }
    | { status: 'error'; error: unknown; items: readonly ChannelMediaItem[] };

const PAGE_SIZE = 40;
/** Paging stops here; older posts are reached by searching instead. */
export const MAX_CHANNEL_ITEMS = 400;
/** The backend rejects longer queries; it counts UTF-8 bytes, not characters. */
const SEARCH_MAX_BYTES = 120;

const searchBuffer = new Uint8Array(SEARCH_MAX_BYTES);

/** Trims a query to the backend's byte limit without splitting a character. */
export function boundedSearch(value: string): string {
    const text = value.trim();
    const { read } = new TextEncoder().encodeInto(text, searchBuffer);
    return text.slice(0, read);
}

/**
 * Paging for one channel at a time. A Wails call cannot be aborted once it has
 * crossed the bridge, so every source or filter change bumps a version and a
 * late answer for an older one is dropped instead of replacing the list.
 */
export function createChannelMediaPager(fetchPage: ChannelMediaFetcher) {
    let version = 0;
    let source: ChannelSource | null = null;
    let search = '';
    let kind: ChannelMediaKind = 'all';
    let nextOffsetId = 0;
    let hasMore = false;
    let loading = false;
    let items: readonly ChannelMediaItem[] = [];
    let view: ChannelMediaView = { status: 'idle' };

    function reset(): void {
        version += 1;
        loading = false;
        nextOffsetId = 0;
        hasMore = false;
        items = [];
        view = { status: 'idle' };
    }

    async function load({ append = false }: { append?: boolean } = {}): Promise<ChannelMediaView> {
        if (!source || loading || (append && !hasMore)) return view;
        const current = ++version;
        const target = source;
        const previous = append ? items : [];
        loading = true;
        view = { status: 'loading', items: previous };
        try {
            const page = await fetchPage({
                channelId: target.channelId,
                offsetId: append ? nextOffsetId : 0,
                limit: PAGE_SIZE,
                search,
                kind,
            });
            if (current !== version) return view;
            if (page.channelId !== target.channelId || page.accountId !== target.accountId
                || page.generation !== target.generation) {
                throw new Error('This channel changed while it was loading. Try again.');
            }
            const seen = new Set(previous.map((item) => item.msgId));
            const fresh: ChannelMediaItem[] = [];
            for (const item of page.items) {
                if (item.msgId <= 0 || seen.has(item.msgId)) continue;
                seen.add(item.msgId);
                fresh.push(item);
            }
            items = [...previous, ...fresh].slice(0, MAX_CHANNEL_ITEMS);
            nextOffsetId = Math.max(0, page.nextOffsetId);
            const capped = items.length >= MAX_CHANNEL_ITEMS;
            hasMore = page.hasMore && nextOffsetId > 0 && !capped;
            view = items.length > 0
                ? { status: 'ready', items, hasMore, capped }
                : { status: 'empty', hasMore };
        } catch (error) {
            if (current === version) view = { status: 'error', error, items: previous };
        } finally {
            if (current === version) loading = false;
        }
        return view;
    }

    return {
        snapshot: (): ChannelMediaView => view,
        load,
        select(next: ChannelSource | null): void {
            source = next;
            reset();
        },
        setFilter(nextSearch: string, nextKind: ChannelMediaKind): void {
            search = boundedSearch(nextSearch);
            kind = nextKind;
            reset();
        },
        invalidate(): void {
            source = null;
            reset();
        },
    };
}

// Telegram posts often carry no filename. The backend then names them after
// the message ("Telegram media 884.mkv"), which says nothing to a reader.
const GENERATED_NAME = /^Telegram media(?: \d+)?(?:\.[a-z0-9]+)?$/i;

function captionLine(item: ChannelMediaItem): string {
    return item.caption.split('\n').map((line) => line.trim()).find(Boolean) ?? '';
}

function fileName(item: ChannelMediaItem): string {
    return GENERATED_NAME.test(item.name) ? '' : item.name.trim();
}

/**
 * The words a reader knows the post by, the way Telegram shows it: a video
 * post is its caption, an audio post is its file name.
 */
export function mediaTitle(item: ChannelMediaItem): string {
    const caption = captionLine(item);
    const name = fileName(item);
    if (item.kind === 'audio') return name || caption || 'Untitled audio';
    if (item.kind === 'video') return caption || name || 'Untitled video';
    return name || caption || 'Untitled file';
}

/** Everything the title leaves out, for the row's tooltip. */
export function mediaDetails(item: ChannelMediaItem): string {
    const name = fileName(item);
    const caption = item.caption.trim();
    return caption && caption !== name ? [name, caption].filter(Boolean).join('\n') : name;
}

export function mediaKindLabel(item: ChannelMediaItem): string {
    if (item.kind === 'video') return 'Video';
    if (item.kind === 'audio') return 'Audio';
    const { ext } = splitNameAndExt(item.name);
    return ext === 'FILE' ? 'File' : ext;
}

/** "Video · 12:04 · 1.2 GB · 3 days ago", dropping whatever is unknown. */
export function mediaMeta(item: ChannelMediaItem, nowMs = Date.now()): string {
    return [
        mediaKindLabel(item),
        item.duration > 0 ? formatDuration(item.duration) : '',
        item.size > 0 ? formatBytes(item.size) : '',
        relativeTimeLabel(item.date, nowMs),
    ].filter(Boolean).join(' · ');
}

const RESTRICTION_LABELS: Readonly<Record<string, string>> = {
    protected: 'Protected',
    paid: 'Paid',
    expires: 'Expiring',
};

/**
 * Why Telegram keeps this post to itself, in one word, or '' when it does
 * not. An unsupported format is not a restriction: the row says what the file
 * is and opens it in Telegram.
 */
export function restrictionLabel(item: ChannelMediaItem): string {
    return RESTRICTION_LABELS[item.blockReason] ?? '';
}

export function isPlayable(item: ChannelMediaItem): boolean {
    return item.streamable && !item.blockReason;
}

export type ChannelSort = 'newest' | 'oldest' | 'name' | 'size' | 'length';

export const CHANNEL_SORTS: ReadonlyArray<{ value: ChannelSort; label: string }> = [
    { value: 'newest', label: 'Newest' },
    { value: 'oldest', label: 'Oldest' },
    { value: 'name', label: 'Name' },
    { value: 'size', label: 'Size' },
    { value: 'length', label: 'Length' },
];

const byName = new Intl.Collator(undefined, { numeric: true, sensitivity: 'base' });

/**
 * Orders the posts loaded so far. Telegram pages a channel newest first, which
 * is also the order they arrive in, so Newest leaves them as they are. Names
 * compare as people read them: episode 2 before episode 10.
 */
export function sortPosts(posts: readonly ChannelMediaItem[], sort: ChannelSort): readonly ChannelMediaItem[] {
    if (sort === 'newest') return posts;
    const sorted = [...posts];
    switch (sort) {
        case 'oldest': sorted.sort((a, b) => a.date - b.date || a.msgId - b.msgId); break;
        case 'name': sorted.sort((a, b) => byName.compare(mediaTitle(a), mediaTitle(b))); break;
        case 'size': sorted.sort((a, b) => b.size - a.size); break;
        case 'length': sorted.sort((a, b) => b.duration - a.duration); break;
    }
    return sorted;
}

/**
 * The player's queue for a video post: the channel's playable videos in the
 * order the list shows them, the way a folder's videos queue in a drive. Every
 * entry opens through its own channel capability, never by message id.
 */
export function channelVideoQueue(
    item: ChannelMediaItem,
    source: ChannelSource,
    posts: readonly ChannelMediaItem[],
    open: (post: ChannelMediaItem) => Promise<MediaOpenResult>,
): { target: VideoOpenTarget; playlist: VideoPlaylistLaunch } {
    const target = (post: ChannelMediaItem): VideoOpenTarget => ({
        id: post.msgId,
        key: `channel:${source.channelId}:${post.msgId}`,
        name: post.name,
        title: mediaTitle(post),
        size: post.size,
        open: () => open(post),
    });
    const videos = posts.filter((post) => post.kind === 'video' && isPlayable(post));
    return {
        target: target(item),
        playlist: {
            items: videos.map(target),
            currentIndex: videos.findIndex((post) => post.msgId === item.msgId),
            title: `Videos in ${source.title}`,
        },
    };
}

/** Public channels have a t.me page; private ones are reached through a post. */
export function channelTelegramUrl(source: ChannelSource): string {
    return source.username ? `https://t.me/${encodeURIComponent(source.username)}` : '';
}

export function channelInitial(title: string): string {
    return Array.from(title.trim())[0]?.toUpperCase() ?? '#';
}
