import { listMediaPage, type GalleryItem, type MediaPage, type MediaTimeline } from '../../api/gallery';

interface SourceOptions {
    maxPages?: number;
    load?: (cursor: string, limit: number) => Promise<MediaPage>;
    onStale?: () => void;
    loadAnchors?: (generation: string) => Promise<MediaTimeline>;
}
interface PageRequest {
    promise: Promise<void>;
    resolve: () => void;
    reject: (error: unknown) => void;
    direct: boolean;
    running: boolean;
}

/** A bounded page LRU and one scheduler for viewport, keyboard and viewer.
 * Month summaries describe the library; full records live only in this cache. */
export class GallerySource {
    readonly timeline: MediaTimeline;
    private readonly pages = new Map<number, GalleryItem[]>();
    private readonly requests = new Map<number, PageRequest>();
    private readonly listeners = new Set<() => void>();
    private readonly load: (cursor: string, limit: number) => Promise<MediaPage>;
    private readonly maxPages: number;
    private readonly onStale?: () => void;
    private readonly loadAnchors?: (generation: string) => Promise<MediaTimeline>;
    private anchorLoad: Promise<void> | null = null;
    private pinned = new Set<number>();
    private disposed = false;
    private stale = false;
    private active = 0;
    error = '';

    constructor(timeline: MediaTimeline, options: SourceOptions = {}) {
        this.timeline = timeline;
        this.maxPages = Math.max(2, options.maxPages ?? 6);
        this.load = options.load ?? listMediaPage;
        this.onStale = options.onStale;
        this.loadAnchors = options.loadAnchors;
    }

    get retainedCount(): number {
        let count = 0;
        for (const page of this.pages.values()) count += page.length;
        return count;
    }
    get pendingCount(): number { return this.requests.size; }

    reportRefreshError(): void {
        if (this.disposed) return;
        this.error = 'Photos changed while loading. Retry to update this view.';
        this.notify();
    }

    subscribe(listener: () => void): () => void {
        this.listeners.add(listener);
        return () => this.listeners.delete(listener);
    }

    peek(index: number): GalleryItem | undefined {
        const start = this.pageStart(index);
        return this.pages.get(start)?.[index - start];
    }

    indexOf(msgId: number): number | undefined {
        for (const [start, items] of this.pages) {
            const offset = items.findIndex((item) => item.msgId === msgId);
            if (offset !== -1) return start + offset;
        }
        return undefined;
    }

    async get(index: number): Promise<GalleryItem | undefined> {
        if (this.disposed || index < 0 || index >= this.timeline.totalCount) return undefined;
        await this.fetchPage(this.pageStart(index), true);
        return this.peek(index);
    }

    /** Keep viewport pages pinned while the viewer walks elsewhere. New scroll
     * intent replaces queued viewport work, never creating one task per row. */
    ensureRange(first: number, last: number): void {
        if (this.disposed) return;
        const wanted: number[] = [];
        for (let start = this.pageStart(Math.max(0, first)); start <= last && start < this.timeline.totalCount; start += this.timeline.pageSize) {
            wanted.push(start);
            if (wanted.length >= this.maxPages - 1) break;
        }
        this.pinned = new Set(wanted);
        for (const [start, request] of this.requests) {
            if (!request.running && !request.direct && !this.pinned.has(start)) this.discard(start, request);
        }
        for (const start of wanted) {
            void this.fetchPage(start, false).catch((error: unknown) => {
                if (this.disposed || !this.pinned.has(start)) return;
                this.error = /stale/i.test(String(error)) ? 'Updating photos…' : 'Could not load these photos.';
                this.notify();
            });
        }
    }

    dispose(): void {
        this.disposed = true;
        for (const [start, request] of this.requests) this.discard(start, request);
        this.pages.clear();
        this.listeners.clear();
    }

    installAnchors(timeline: MediaTimeline): void {
        if (this.disposed) return;
        if (timeline.generation !== this.timeline.generation || timeline.channelId !== this.timeline.channelId
            || timeline.totalCount !== this.timeline.totalCount || timeline.pageSize !== this.timeline.pageSize) {
            this.failAnchors(new Error('gallery snapshot is stale'));
            return;
        }
        this.timeline.anchors = timeline.anchors.slice();
        this.pump();
        this.notify();
    }

    requestAnchors(): Promise<void> {
        if (this.disposed || this.timeline.anchors.length > 0 || !this.loadAnchors) return Promise.resolve();
        if (this.anchorLoad) return this.anchorLoad;
        this.anchorLoad = this.loadAnchors(this.timeline.generation)
            .then((timeline) => this.installAnchors(timeline))
            .catch((error: unknown) => {
                this.failAnchors(error);
                if (!this.disposed && !this.stale && /stale/i.test(String(error))) {
                    this.stale = true;
                    this.onStale?.();
                }
                throw error;
            })
            .finally(() => { this.anchorLoad = null; });
        return this.anchorLoad;
    }

    failAnchors(error: unknown): void {
        for (const [start, request] of this.requests) {
            if (!request.running && start > 0) {
                this.requests.delete(start);
                request.reject(error);
            }
        }
    }

    private pageStart(index: number): number { return Math.floor(index / this.timeline.pageSize) * this.timeline.pageSize; }

    private touch(start: number): void {
        const page = this.pages.get(start);
        if (!page) return;
        this.pages.delete(start);
        this.pages.set(start, page);
    }

    private fetchPage(start: number, direct: boolean): Promise<void> {
        if (this.pages.has(start)) { this.touch(start); return Promise.resolve(); }
        const existing = this.requests.get(start);
        if (existing) { existing.direct ||= direct; return existing.promise; }
        if (start > 0 && !this.timeline.anchors[start / this.timeline.pageSize]) {
            void this.requestAnchors().catch(() => { /* A later demand retries transient failures. */ });
            return this.queuePage(start, direct);
        }
        return this.queuePage(start, direct);
    }

    private queuePage(start: number, direct: boolean): Promise<void> {
        // Rapid keyboard repeats retain only recent queued intent. Running
        // SQLite reads finish, but there are never more than two of them.
        const queued = [...this.requests].filter(([, request]) => !request.running);
        if (queued.length >= this.maxPages) {
            const candidate = queued.find(([key]) => !this.pinned.has(key)) ?? queued[0];
            this.discard(...candidate);
        }
        let resolve!: () => void;
        let reject!: (error: unknown) => void;
        const promise = new Promise<void>((done, fail) => { resolve = done; reject = fail; });
        this.requests.set(start, { promise, resolve, reject, direct, running: false });
        this.pump();
        return promise;
    }

    private discard(start: number, request: PageRequest): void {
        this.requests.delete(start);
        request.resolve();
    }

    private pump(): void {
        while (!this.disposed && this.active < 2) {
            const pending = [...this.requests].filter(([start, request]) => !request.running
                && (start === 0 || Boolean(this.timeline.anchors[start / this.timeline.pageSize])));
            const next = pending.find(([, request]) => request.direct) ?? pending[0];
            if (!next) return;
            const [start, request] = next;
            request.running = true;
            this.active += 1;
            void this.run(start, request);
        }
    }

    private async run(start: number, request: PageRequest): Promise<void> {
        try {
            const anchor = this.timeline.anchors[start / this.timeline.pageSize];
            const page = await this.load(start === 0 ? (anchor?.cursor ?? '') : anchor.cursor, this.timeline.pageSize);
            if (this.disposed) return;
            if (page.generation !== this.timeline.generation) throw new Error('gallery snapshot is stale');
            const expected = Math.min(this.timeline.pageSize, this.timeline.totalCount - start);
            if (page.startIndex !== start || page.items.length !== expected) throw new Error('Invalid gallery page position');
            this.pages.set(start, page.items);
            for (const key of this.pages.keys()) {
                if (this.pages.size <= this.maxPages) break;
                if (!this.pinned.has(key)) this.pages.delete(key);
            }
            this.error = '';
            this.notify();
            request.resolve();
        } catch (error) {
            if (!this.disposed && !this.stale && /gallery snapshot is stale/i.test(String(error))) {
                this.stale = true;
                this.onStale?.();
            }
            request.reject(error);
        } finally {
            if (this.requests.get(start) === request) this.requests.delete(start);
            this.active -= 1;
            this.pump();
        }
    }

    private notify(): void { for (const listener of this.listeners) listener(); }
}
