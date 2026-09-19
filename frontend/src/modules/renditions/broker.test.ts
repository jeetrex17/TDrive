import { describe, expect, it, vi } from 'vitest';
import { RenditionBroker, type RenditionRequest, type RenditionLoader } from './broker';

const request: RenditionRequest = { scope: 'account-session', channelId: 10, fileId: 1, revision: 1, kind: 'thumbnail' };
const limits = { concurrency: 1, decodedBytes: 4 * 1024 * 1024, compressedBytes: 2 * 1024 * 1024, maxQueued: 8 };
const result = (size = 10) => ({ blob: new Blob([new Uint8Array(size)], { type: 'image/jpeg' }), width: 32, height: 32 });
function deferred<T>() {
    let resolve!: (value: T) => void;
    let reject!: (reason: unknown) => void;
    const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
    return { promise, resolve, reject };
}
function setup(loader: RenditionLoader = vi.fn(async () => result())) {
    let next = 0;
    const revoke = vi.fn();
    const broker = new RenditionBroker(loader, limits, { create: () => `blob:${++next}`, revoke });
    return { broker, loader, revoke };
}

describe('shared rendition broker', () => {
    it('shares work and keeps it alive until its last consumer releases', async () => {
        const pending = deferred<ReturnType<typeof result>>();
        const loader = vi.fn((_request: RenditionRequest, _signal: AbortSignal) => pending.promise);
        const { broker } = setup(loader);
        const first = broker.acquire(request, 'visible');
        const second = broker.acquire(request, 'viewer');
        first.release();
        await expect(first.promise).rejects.toMatchObject({ name: 'AbortError' });
        expect(loader).toHaveBeenCalledTimes(1);
        expect(loader.mock.calls[0][1].aborted).toBe(false);
        pending.resolve(result());
        expect((await second.promise).url).toBe('blob:1');
        second.release();
        broker.dispose();
    });

    it('aborts abandoned work and cannot cache its late completion', async () => {
        const pending = deferred<ReturnType<typeof result>>();
        const loader = vi.fn((_request: RenditionRequest, _signal: AbortSignal) => pending.promise);
        const { broker, revoke } = setup(loader);
        const lease = broker.acquire(request, 'visible');
        lease.release();
        expect(loader.mock.calls[0][1].aborted).toBe(true);
        pending.resolve(result());
        await expect(lease.promise).rejects.toMatchObject({ name: 'AbortError' });
        await Promise.resolve();
        expect(broker.stats().entries).toBe(0);
        expect(revoke).not.toHaveBeenCalled();
    });

    it('separates revisions, account sessions and drives', async () => {
        const { broker, loader } = setup();
        for (const variation of [request, { ...request, revision: 2 }, { ...request, scope: 'next-account' }, { ...request, channelId: 11 }]) {
            const lease = broker.acquire(variation, 'visible');
            await lease.promise;
            lease.release();
        }
        expect(loader).toHaveBeenCalledTimes(4);
        broker.dispose();
    });

    it('prioritizes the viewer over queued speculative work and caps the queue', async () => {
        const pending = deferred<ReturnType<typeof result>>();
        const loader = vi.fn(async (value: RenditionRequest) => value.fileId === 1 ? pending.promise : result());
        const { broker } = setup(loader);
        const active = broker.acquire(request, 'visible');
        const prefetch = broker.acquire({ ...request, fileId: 2 }, 'prefetch');
        const viewer = broker.acquire({ ...request, fileId: 3 }, 'viewer');
        pending.resolve(result());
        await Promise.all([active.promise, viewer.promise, prefetch.promise]);
        expect(loader.mock.calls.map(([value]) => value.fileId)).toEqual([1, 3, 2]);
        broker.dispose();
    });

    it('bounds decoded memory and evicts unpinned least recently used results', async () => {
        const revoke = vi.fn();
        const broker = new RenditionBroker(async () => ({ ...result(), width: 512, height: 512 }), { ...limits, decodedBytes: 1024 * 1024 }, { create: () => 'blob:test', revoke });
        const first = broker.acquire(request, 'visible');
        await first.promise;
        const second = broker.acquire({ ...request, fileId: 2 }, 'visible');
        expect(broker.stats().running).toBe(0);
        first.release();
        await second.promise;
        expect(revoke).toHaveBeenCalledTimes(1);
        expect(broker.stats().decodedBytes).toBeLessThanOrEqual(1024 * 1024);
        broker.dispose();
    });

    it('rejects oversized renditions before publishing an image URL', async () => {
        const { broker } = setup(vi.fn(async () => ({ ...result(), width: 100_000, height: 100_000 })));
        await expect(broker.acquire(request, 'visible').promise).rejects.toThrow('budget');
        expect(broker.stats().entries).toBe(0);
    });

    it('bounds cache entry metadata even for thousands of tiny images', async () => {
        const { broker, revoke } = setup(vi.fn(async () => ({ ...result(), width: 1, height: 1 })));
        for (let fileId = 1; fileId <= 1000; fileId += 1) {
            const lease = broker.acquire({ ...request, fileId }, 'visible');
            await lease.promise;
            lease.release();
        }
        expect(broker.stats().entries).toBeLessThanOrEqual(limits.maxQueued * 2);
        expect(revoke).toHaveBeenCalled();
        broker.dispose();
    });

    it('does not retain results with unknown content revisions', async () => {
        const { broker } = setup();
        const lease = broker.acquire({ ...request, revision: 0 }, 'viewer');
        await lease.promise;
        lease.release();
        expect(broker.stats().entries).toBe(0);
    });

    it('cancels speculative work and changes admission limits', async () => {
        const pending = deferred<ReturnType<typeof result>>();
        const { broker } = setup(vi.fn(async () => pending.promise));
        const lease = broker.acquire(request, 'prefetch');
        broker.cancelPrefetch();
        await expect(lease.promise).rejects.toMatchObject({ name: 'AbortError' });
        pending.resolve(result());
        await Promise.resolve();
        broker.setLimits({ ...limits, decodedBytes: 1 });
        await expect(broker.acquire({ ...request, fileId: 2 }, 'visible').promise).rejects.toThrow('budget');
        await expect(broker.acquire({ ...request, fileId: -1 }, 'visible').promise).rejects.toThrow('identity');
        broker.dispose();
    });

    it('disposes queued and running work and revokes ready URLs', async () => {
        const { broker, revoke } = setup();
        const lease = broker.acquire(request, 'visible');
        await lease.promise;
        broker.dispose();
        expect(revoke).toHaveBeenCalledWith('blob:1');
        await expect(broker.acquire(request, 'visible').promise).rejects.toThrow('closed');
    });
});
