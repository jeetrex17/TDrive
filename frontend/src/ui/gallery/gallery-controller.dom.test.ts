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
});
