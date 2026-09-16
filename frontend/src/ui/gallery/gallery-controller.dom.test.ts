// Unit tests for the gallery controller: lazy load on intersect, thumb-cache
// hits, drive-change and unregister discard guards, and FIFO eviction of
// decoded images. getThumbnail is mocked; a manual IntersectionObserver shim
// lets the test drive intersections deterministically.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { CellPatch } from './gallery-controller';

const thumbnails = vi.hoisted(() => ({
    resolver: (msgId: number) => Promise.resolve(`data:url:${msgId}`),
}));

vi.mock('../../api', () => ({
    getThumbnail: (msgId: number) => thumbnails.resolver(msgId),
}));

// Manual IntersectionObserver: records observed nodes and exposes a trigger to
// fire an intersection for a specific node.
const observed = new Set<Element>();
let fireIntersect: (node: Element) => void = () => {};

class TestObserver {
    constructor(private cb: IntersectionObserverCallback) {
        fireIntersect = (node: Element) => {
            this.cb(
                [{ target: node, isIntersecting: true } as IntersectionObserverEntry],
                this as unknown as IntersectionObserver,
            );
        };
    }
    observe(node: Element) { observed.add(node); }
    unobserve(node: Element) { observed.delete(node); }
    disconnect() { observed.clear(); }
}

(globalThis as any).IntersectionObserver = TestObserver;

let controller: typeof import('./gallery-controller');

async function flush(): Promise<void> {
    await Promise.resolve();
    await Promise.resolve();
}

function makeCell(msgId: number) {
    const node = document.createElement('button');
    const patches: CellPatch[] = [];
    const apply = (patch: CellPatch): void => {
        patches.push(patch);
    };
    return { node, msgId, apply, patches, last: () => patches[patches.length - 1] };
}

beforeEach(async () => {
    vi.resetModules();
    observed.clear();
    thumbnails.resolver = (msgId: number) => Promise.resolve(`data:url:${msgId}`);
    controller = await import('./gallery-controller');
    controller.setRoot(document.createElement('div'));
    controller.beginRender(1);
});

afterEach(() => {
    vi.restoreAllMocks();
});

describe('gallery-controller', () => {
    it('loads a thumbnail when a cell intersects and caches it', async () => {
        const cell = makeCell(10);
        controller.registerCell(cell.node, { msgId: cell.msgId, apply: cell.apply });
        expect(observed.has(cell.node)).toBe(true);

        fireIntersect(cell.node);
        await flush();

        expect(cell.last()).toEqual({ status: 'loaded', src: 'data:url:10' });
        expect(observed.has(cell.node)).toBe(false); // unobserved once loaded
        expect(controller.cachedThumb(1, 10)).toBe('data:url:10');
    });

    it('serves a cache hit without calling the backend again', async () => {
        const first = makeCell(20);
        controller.registerCell(first.node, { msgId: 20, apply: first.apply });
        fireIntersect(first.node);
        await flush();

        const calls = vi.fn();
        thumbnails.resolver = (msgId: number) => { calls(); return Promise.resolve(`data:url:${msgId}`); };

        const second = makeCell(20);
        controller.registerCell(second.node, { msgId: 20, apply: second.apply });
        fireIntersect(second.node);
        await flush();

        expect(second.last()).toEqual({ status: 'loaded', src: 'data:url:20' });
        expect(calls).not.toHaveBeenCalled();
    });

    it('discards a load whose cell was unregistered mid-flight', async () => {
        let resolve!: (v: string) => void;
        thumbnails.resolver = () => new Promise<string>((r) => { resolve = r; });

        const cell = makeCell(30);
        controller.registerCell(cell.node, { msgId: 30, apply: cell.apply });
        fireIntersect(cell.node);
        await flush();
        expect(cell.last()).toEqual({ status: 'loading' });

        controller.unregisterCell(cell.node);
        resolve('data:url:30');
        await flush();

        // No 'loaded' patch after unregister, and nothing cached.
        expect(cell.patches.some((p) => p.status === 'loaded')).toBe(false);
        expect(controller.cachedThumb(1, 30)).toBe('');
    });

    it('ignores a stale completion when a keyed cell is registered for another item', async () => {
        let resolveOld!: (value: string) => void;
        let resolveNew!: (value: string) => void;
        let request = 0;
        thumbnails.resolver = () => new Promise<string>((resolve) => {
            if (request++ === 0) resolveOld = resolve;
            else resolveNew = resolve;
        });

        const oldCell = makeCell(31);
        controller.registerCell(oldCell.node, { msgId: 31, apply: oldCell.apply });
        fireIntersect(oldCell.node);
        await flush();
        expect(oldCell.last()).toEqual({ status: 'loading' });

        const replacementPatches: CellPatch[] = [];
        controller.registerCell(oldCell.node, {
            msgId: 32,
            apply: (patch) => replacementPatches.push(patch),
        });
        fireIntersect(oldCell.node);
        await flush();

        resolveOld('data:url:31');
        await flush();
        expect(oldCell.patches.some((patch) => patch.status === 'loaded')).toBe(false);
        expect(replacementPatches.some((patch) => patch.status === 'loaded')).toBe(false);
        expect(controller.cachedThumb(1, 31)).toBe('');

        resolveNew('data:url:32');
        await flush();
        expect(replacementPatches[replacementPatches.length - 1]).toEqual({ status: 'loaded', src: 'data:url:32' });
    });

    it('discards a load when the drive changed mid-flight', async () => {
        let resolve!: (v: string) => void;
        thumbnails.resolver = () => new Promise<string>((r) => { resolve = r; });

        const cell = makeCell(40);
        controller.registerCell(cell.node, { msgId: 40, apply: cell.apply });
        fireIntersect(cell.node);
        await flush();

        controller.beginRender(2); // user switched drives
        resolve('data:url:40');
        await flush();

        expect(cell.patches.some((p) => p.status === 'loaded')).toBe(false);
    });

    it('marks a locked cell and rearms it on unlock', async () => {
        thumbnails.resolver = () => Promise.reject(new Error('encryption password required'));

        const cell = makeCell(50);
        controller.registerCell(cell.node, { msgId: 50, apply: cell.apply });
        fireIntersect(cell.node);
        await flush();

        expect(cell.last()).toMatchObject({ status: 'locked' });
        expect(observed.has(cell.node)).toBe(false);

        thumbnails.resolver = (msgId: number) => Promise.resolve(`data:url:${msgId}`);
        controller.rearmLocked();
        expect(observed.has(cell.node)).toBe(true); // re-observed for retry

        fireIntersect(cell.node);
        await flush();
        expect(cell.last()).toEqual({ status: 'loaded', src: 'data:url:50' });
    });

    it('keeps a rate-limited cell shimmering and retries after the wait Telegram named', async () => {
        vi.useFakeTimers();
        try {
            let calls = 0;
            thumbnails.resolver = (msgId: number) => {
                calls += 1;
                return calls === 1
                    ? Promise.reject(new Error('tgclient: GetFileDocument failed: tgclient: flood wait: 25s'))
                    : Promise.resolve(`data:url:${msgId}`);
            };

            const cell = makeCell(60);
            controller.registerCell(cell.node, { msgId: 60, apply: cell.apply });
            fireIntersect(cell.node);
            await flush();
            expect(cell.last()).toEqual({ status: 'loading' });
            expect(cell.patches.some((patch) => patch.status === 'failed')).toBe(false);

            await vi.advanceTimersByTimeAsync(25_000);
            expect(calls).toBe(1);
            await vi.advanceTimersByTimeAsync(1_100);
            await flush();
            expect(calls).toBe(2);
            expect(cell.last()).toEqual({ status: 'loaded', src: 'data:url:60' });
        } finally {
            vi.useRealTimers();
        }
    });

    it('fails a cell outright for a permanent error and after the retry budget', async () => {
        vi.useFakeTimers();
        try {
            thumbnails.resolver = () => Promise.reject(new Error('unsupported image'));
            const permanent = makeCell(70);
            controller.registerCell(permanent.node, { msgId: 70, apply: permanent.apply });
            fireIntersect(permanent.node);
            await flush();
            expect(permanent.last()).toMatchObject({ status: 'failed' });

            let calls = 0;
            thumbnails.resolver = () => {
                calls += 1;
                return Promise.reject(new Error('flood wait: 1s'));
            };
            const limited = makeCell(71);
            controller.registerCell(limited.node, { msgId: 71, apply: limited.apply });
            fireIntersect(limited.node);
            await flush();
            for (let round = 0; round < 3; round += 1) {
                await vi.advanceTimersByTimeAsync(2_100);
                await flush();
            }
            expect(calls).toBe(4);
            expect(limited.last()).toMatchObject({ status: 'failed' });
        } finally {
            vi.useRealTimers();
        }
    });

    it('loads at most three thumbnails at a time', async () => {
        const resolvers: Array<(value: string) => void> = [];
        thumbnails.resolver = () => new Promise<string>((resolve) => { resolvers.push(resolve); });

        const cells = [80, 81, 82, 83, 84].map((msgId) => {
            const cell = makeCell(msgId);
            controller.registerCell(cell.node, { msgId, apply: cell.apply });
            fireIntersect(cell.node);
            return cell;
        });
        await flush();
        expect(resolvers).toHaveLength(3);
        expect(cells.every((cell) => cell.last()?.status === 'loading')).toBe(true);

        resolvers[0]('data:url:80');
        await flush();
        await flush();
        expect(resolvers).toHaveLength(4);
        expect(cells[0].last()).toEqual({ status: 'loaded', src: 'data:url:80' });
    });
});
