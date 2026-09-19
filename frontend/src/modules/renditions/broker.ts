/** The broker owns binary image URLs. Callers own leases, never the URLs. */
/** The rendition broker is intentionally limited to bounded thumbnails. */
export type RenditionKind = 'thumbnail';
export type RenditionPriority = 'viewer' | 'visible' | 'prefetch';
export interface RenditionRequest {
    scope: string;
    channelId: number;
    fileId: number;
    revision: number;
    kind: RenditionKind;
}
export interface RenditionAsset { url: string; width: number; height: number }
export interface RenditionLease { promise: Promise<RenditionAsset>; release(): void }
export interface RenditionBytes { blob: Blob; width: number; height: number }
export interface RenditionLimits { concurrency: number; decodedBytes: number; compressedBytes: number; maxQueued: number }
export type RenditionLoader = (request: RenditionRequest, signal: AbortSignal) => Promise<RenditionBytes>;
type Subscriber = { resolve: (asset: RenditionAsset) => void; reject: (error: unknown) => void };
type Entry = {
    key: string;
    request: RenditionRequest;
    priority: RenditionPriority;
    controller: AbortController;
    subscribers: Set<Subscriber>;
    status: 'queued' | 'running' | 'ready';
    asset?: RenditionAsset;
    decodedBytes: number;
    compressedBytes: number;
};
const priorities: Record<RenditionPriority, number> = { viewer: 0, visible: 1, prefetch: 2 };
const MAX_COMPRESSED = { thumbnail: 1024 * 1024 };
const MAX_EDGE = { thumbnail: 512 };
export const renditionMaxBytes = (kind: RenditionKind): number => MAX_COMPRESSED[kind];
export const renditionMaxEdge = (kind: RenditionKind): number => MAX_EDGE[kind];
export const renditionKey = (request: RenditionRequest): string => JSON.stringify([request.scope, request.channelId, request.fileId, request.revision, request.kind]);
const aborted = (): DOMException => new DOMException('Image request released.', 'AbortError');

/**
 * One priority queue and byte budget for grid and viewer. The queue is small
 * and capped, so a linear scan avoids a second heap/index that could drift
 * during cancellation. Map iteration gives true O(1) LRU touches and removal.
 */
export class RenditionBroker {
    private readonly entries = new Map<string, Entry>();
    private running = 0;
    private decodedBytes = 0;
    private compressedBytes = 0;
    private closed = false;

    constructor(
        private readonly load: RenditionLoader,
        private limits: RenditionLimits,
        private readonly urls = { create: (blob: Blob) => URL.createObjectURL(blob), revoke: (url: string) => URL.revokeObjectURL(url) },
    ) {}

    acquire(request: RenditionRequest, priority: RenditionPriority): RenditionLease {
        if (this.closed) return this.rejected(new Error('Image session is closed.'));
        if (!Object.prototype.hasOwnProperty.call(MAX_EDGE, request.kind) || !request.scope || !Number.isSafeInteger(request.channelId) || request.channelId === 0
            || !Number.isSafeInteger(request.fileId) || request.fileId <= 0
            || !Number.isSafeInteger(request.revision) || request.revision < 0) {
            return this.rejected(new Error('Invalid image identity.'));
        }
        const key = renditionKey(request);
        let entry = this.entries.get(key);
        if (!entry) {
            // A byte budget alone would retain unbounded metadata for 1px
            // images. Count ready entries and URL handles as separate resources.
            if (!this.makeEntryRoom()) return this.rejected(new Error('Image queue is full.'));
            if (this.stats().queued >= this.limits.maxQueued) return this.rejected(new Error('Image queue is full.'));
            entry = { key, request: { ...request }, priority, controller: new AbortController(), subscribers: new Set(), status: 'queued', decodedBytes: 0, compressedBytes: 0 };
            this.entries.set(key, entry);
        }
        if (priorities[priority] < priorities[entry.priority]) entry.priority = priority;
        this.touch(entry);
        const owned = entry;
        let released = false;
        let subscriber!: Subscriber;
        const promise = new Promise<RenditionAsset>((resolve, reject) => {
            subscriber = { resolve, reject };
            owned.subscribers.add(subscriber);
            if (owned.asset) resolve(owned.asset);
        });
        // A viewport may release a queued cell before its effect awaits it.
        void promise.catch(() => {});
        this.pump();
        return { promise, release: () => {
            if (released) return;
            released = true;
            owned.subscribers.delete(subscriber);
            subscriber.reject(aborted());
            if (owned.subscribers.size === 0 && (owned.status !== 'ready' || owned.request.revision === 0)) this.remove(owned);
            this.pump();
        } };
    }

    setLimits(limits: RenditionLimits): void {
        this.limits = { ...limits };
        this.evictUntilFits(0, 0);
        this.pump();
    }

    cancelPrefetch(): void {
        for (const entry of this.entries.values()) {
            if (entry.priority === 'prefetch') this.remove(entry);
        }
        this.pump();
    }

    dispose(): void {
        this.closed = true;
        for (const entry of this.entries.values()) this.remove(entry);
    }

    stats(): { entries: number; queued: number; running: number; decodedBytes: number; compressedBytes: number } {
        return { entries: this.entries.size, queued: [...this.entries.values()].filter(entry => entry.status === 'queued').length, running: this.running, decodedBytes: this.decodedBytes, compressedBytes: this.compressedBytes };
    }

    private rejected(error: Error): RenditionLease {
        const promise = Promise.reject<RenditionAsset>(error);
        void promise.catch(() => {});
        return { promise, release: () => {} };
    }

    private touch(entry: Entry): void {
        this.entries.delete(entry.key);
        this.entries.set(entry.key, entry);
    }

    private pump(): void {
        if (this.closed) return;
        while (this.running < this.limits.concurrency) {
            let next: Entry | undefined;
            for (const entry of this.entries.values()) {
                if (entry.status === 'queued' && (!next || priorities[entry.priority] < priorities[next.priority])) next = entry;
            }
            if (!next) return;
            const decoded = MAX_EDGE[next.request.kind] ** 2 * 4;
            const compressed = MAX_COMPRESSED[next.request.kind];
            if (decoded > this.limits.decodedBytes || compressed > this.limits.compressedBytes) {
                this.fail(next, new Error('Image exceeds the memory budget.'));
                continue;
            }
            if (!this.evictUntilFits(decoded, compressed)) return;
            next.status = 'running';
            next.decodedBytes = decoded;
            next.compressedBytes = compressed;
            this.decodedBytes += decoded;
            this.compressedBytes += compressed;
            this.running += 1;
            void this.run(next);
        }
    }

    private async run(entry: Entry): Promise<void> {
        try {
            const result = await this.load(entry.request, entry.controller.signal);
            if (this.closed || entry.controller.signal.aborted || entry.subscribers.size === 0) return;
            const { width, height, blob } = result;
            const edge = MAX_EDGE[entry.request.kind];
            if (!Number.isInteger(width) || !Number.isInteger(height) || width < 1 || height < 1
                || width > edge || height > edge || blob.size > MAX_COMPRESSED[entry.request.kind]) {
                throw new Error('Image exceeds the memory budget.');
            }
            this.decodedBytes += width * height * 4 - entry.decodedBytes;
            this.compressedBytes += blob.size - entry.compressedBytes;
            entry.decodedBytes = width * height * 4;
            entry.compressedBytes = blob.size;
            entry.asset = { url: this.urls.create(blob), width, height };
            entry.status = 'ready';
            for (const subscriber of entry.subscribers) subscriber.resolve(entry.asset);
        } catch (error) {
            this.fail(entry, error);
        } finally {
            this.running -= 1;
            // Keep in-flight reservations until cancellation actually settles;
            // otherwise repeated fast scrolls could exceed the physical budget.
            if (entry.status !== 'ready') this.unreserve(entry);
            this.pump();
        }
    }

    private makeEntryRoom(): boolean {
        const maximum = this.limits.maxQueued * 2;
        if (this.entries.size < maximum) return true;
        for (const entry of this.entries.values()) {
            if (entry.status === 'ready' && entry.subscribers.size === 0) this.remove(entry);
            if (this.entries.size < maximum) return true;
        }
        return false;
    }

    private evictUntilFits(decoded: number, compressed: number): boolean {
        const fits = () => this.decodedBytes + decoded <= this.limits.decodedBytes && this.compressedBytes + compressed <= this.limits.compressedBytes;
        if (fits()) return true;
        for (const entry of this.entries.values()) {
            if (entry.status === 'ready' && entry.subscribers.size === 0) this.remove(entry);
            if (fits()) return true;
        }
        return false;
    }

    private unreserve(entry: Entry): void {
        this.decodedBytes -= entry.decodedBytes;
        this.compressedBytes -= entry.compressedBytes;
        entry.decodedBytes = 0;
        entry.compressedBytes = 0;
    }

    private fail(entry: Entry, error: unknown): void {
        for (const subscriber of entry.subscribers) subscriber.reject(error);
        this.remove(entry);
    }

    private remove(entry: Entry): void {
        if (this.entries.get(entry.key) === entry) this.entries.delete(entry.key);
        entry.controller.abort();
        for (const subscriber of entry.subscribers) subscriber.reject(aborted());
        entry.subscribers.clear();
        if (entry.asset) {
            this.urls.revoke(entry.asset.url);
            entry.asset = undefined;
        }
        if (entry.status !== 'running') this.unreserve(entry);
    }
}
