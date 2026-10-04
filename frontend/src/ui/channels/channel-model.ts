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
    mimeType: string;
    kind: Exclude<ChannelMediaKind, 'all'> | string;
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
    | { kind: 'idle' }
    | { kind: 'loading'; items: readonly ChannelMediaItem[] }
    | { kind: 'ready'; items: readonly ChannelMediaItem[]; hasMore: boolean }
    | { kind: 'empty'; hasMore: boolean }
    | { kind: 'error'; message: string; items: readonly ChannelMediaItem[] };

const PAGE_SIZE = 40;
const MAX_RECORDS = 200;

function boundedUTF8(value: string, maxBytes: number): string {
    const text = value.trim();
    const encoder = new TextEncoder();
    if (encoder.encode(text).length <= maxBytes) return text;
    let end = text.length;
    while (end > 0 && encoder.encode(text.slice(0, end)).length > maxBytes) end -= 1;
    // Avoid returning a dangling high surrogate when the byte trim lands in a
    // Unicode code point. slice by code units is otherwise enough here.
    if (end > 0 && /[\uD800-\uDBFF]/.test(text.charAt(end - 1))) end -= 1;
    return text.slice(0, end);
}

/**
 * Bounded, request-versioned paging for a read-only channel. Wails calls cannot
 * be aborted reliably after crossing the bridge, so a newer source/filter/query
 * invalidates the response before it can change the visible list.
 */
export function createChannelMediaPager(fetchPage: ChannelMediaFetcher) {
    let requestVersion = 0;
    let source: ChannelSource | null = null;
    let search = '';
    let kind: ChannelMediaKind = 'all';
    let nextOffsetId = 0;
    let hasMore = false;
    let loading = false;
    let items: readonly ChannelMediaItem[] = [];
    let view: ChannelMediaView = { kind: 'idle' };

    const snapshot = (): ChannelMediaView => view;

    async function load({ append = false }: { append?: boolean } = {}): Promise<ChannelMediaView> {
        if (!source || loading || (append && !hasMore)) return snapshot();
        const version = ++requestVersion;
        const previous = append ? items : [];
        loading = true;
        view = { kind: 'loading', items: previous };
        try {
            const page = await fetchPage({
                channelId: source.channelId,
                offsetId: append ? nextOffsetId : 0,
                limit: PAGE_SIZE,
                search,
                kind,
            });
            // Account/source generations are a separate target identity. Do not
            // publish a response after reconnect, disconnect, or source switch.
            if (version !== requestVersion || !source || page.channelId !== source.channelId
                || page.accountId !== source.accountId || page.generation !== source.generation) return snapshot();
            const byId = new Set<number>();
            const merged = [...previous, ...page.items].filter((item) => {
                if (!Number.isSafeInteger(item.msgId) || item.msgId <= 0 || byId.has(item.msgId)) return false;
                byId.add(item.msgId);
                return true;
            });
            items = Object.freeze(merged.slice(-MAX_RECORDS));
            nextOffsetId = Math.max(0, Number(page.nextOffsetId) || 0);
            hasMore = Boolean(page.hasMore && nextOffsetId > 0);
            view = items.length > 0
                ? { kind: 'ready', items, hasMore }
                : { kind: 'empty', hasMore };
        } catch (error) {
            if (version === requestVersion) {
                view = { kind: 'error', message: error instanceof Error ? error.message : 'Could not load channel media.', items: previous };
            }
        } finally {
            if (version === requestVersion) loading = false;
        }
        return snapshot();
    }

    function select(next: ChannelSource | null): ChannelMediaView {
        requestVersion += 1;
        // The bridge request continues in the background, but it no longer
        // owns this view. Allow the newly selected source to start immediately.
        loading = false;
        source = next;
        nextOffsetId = 0;
        hasMore = false;
        items = [];
        view = next ? { kind: 'idle' } : { kind: 'idle' };
        return snapshot();
    }

    function setFilter(nextSearch: string, nextKind: ChannelMediaKind): ChannelMediaView {
        requestVersion += 1;
        loading = false;
        search = boundedUTF8(nextSearch, 120);
        kind = nextKind;
        nextOffsetId = 0;
        hasMore = false;
        items = [];
        view = source ? { kind: 'idle' } : { kind: 'idle' };
        return snapshot();
    }

    function invalidate(): void {
        requestVersion += 1;
        loading = false;
        source = null;
        items = [];
        nextOffsetId = 0;
        hasMore = false;
        view = { kind: 'idle' };
    }

    return { snapshot, select, setFilter, load, invalidate };
}
