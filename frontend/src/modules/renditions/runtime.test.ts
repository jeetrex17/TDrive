import { beforeEach, describe, expect, it, vi } from 'vitest';
const mocks = vi.hoisted(() => ({
    open: vi.fn(), close: vi.fn(), fetch: vi.fn(), policyListener: null as null | ((policy: unknown) => void),
    callbacks: new Map<string, () => void>(),
}));
vi.mock('../../api/renditions', async () => {
    const actual = await vi.importActual<typeof import('../../api/renditions')>('../../api/renditions');
    return { ...actual, openGalleryImages: mocks.open, closeGalleryImages: mocks.close, fetchRendition: mocks.fetch };
});
vi.mock('../../api/runtime', () => ({ onRuntimeEvent: (event: string, callback: () => void) => { mocks.callbacks.set(event, callback); return () => {}; } }));
vi.mock('../gallery-policy', () => ({
    getGalleryPolicy: () => ({ concurrency: 2, decodedBytes: 80 * 1024 * 1024, compressedBytes: 8 * 1024 * 1024, maxQueued: 192, allowPrefetch: false, backgrounded: false }),
    subscribeGalleryPolicy: (callback: (policy: unknown) => void) => { mocks.policyListener = callback; return () => {}; },
}));
import { state } from '../../state';
import { RenditionError } from '../../api/renditions';
const request = { channelId: 10, fileId: 1, revision: 1, kind: 'thumbnail' as const };
beforeEach(() => {
    vi.resetModules();
    vi.clearAllMocks();
    mocks.callbacks.clear();
    mocks.open.mockResolvedValue({ channelId: 10, token: 'session', baseUrl: 'http://127.0.0.1/images' });
    mocks.close.mockResolvedValue(undefined);
    mocks.fetch.mockResolvedValue({ blob: new Blob(['test']), width: 32, height: 32 });
    state.myUserID = 1;
});

describe('rendition runtime lifecycle', () => {
    it('opens one backend session for shared consumers and revokes on lock', async () => {
        const runtime = await import('./runtime');
        const first = runtime.acquireRendition(request, 'visible');
        const second = runtime.acquireRendition(request, 'viewer');
        await Promise.all([first.promise, second.promise]);
        expect(mocks.open).toHaveBeenCalledTimes(1);
        expect(mocks.fetch).toHaveBeenCalledTimes(1);
        const reset = vi.fn();
        const unsubscribe = runtime.subscribeRenditionReset(reset);
        mocks.callbacks.get('encrypted_media_sessions_closed')!();
        await vi.waitFor(() => expect(mocks.close).toHaveBeenCalledWith('session'));
        expect(reset).toHaveBeenCalledOnce();
        expect(runtime.renditionStats()).toBeNull();
        unsubscribe();
        runtime.teardownRenditions();
    });

    it('closes a session that finishes opening after reset without fetching pixels', async () => {
        let resolve!: (value: unknown) => void;
        mocks.open.mockReturnValue(new Promise(yes => { resolve = yes; }));
        const runtime = await import('./runtime');
        const lease = runtime.acquireRendition(request, 'visible');
        runtime.resetRenditions();
        const opened = { channelId: 10, token: crypto.randomUUID(), baseUrl: 'http://127.0.0.1/images' };
        resolve(opened);
        await expect(lease.promise).rejects.toMatchObject({ name: 'AbortError' });
        await vi.waitFor(() => expect(mocks.close).toHaveBeenCalledWith(opened.token));
        expect(mocks.fetch).not.toHaveBeenCalled();
        runtime.teardownRenditions();
    });

    it('honors the complete server cooldown across new image requests', async () => {
        const runtime = await import('./runtime');
        mocks.fetch.mockRejectedValue(new RenditionError('rate_limited', 'wait', 3_701_000));
        await expect(runtime.acquireRendition(request, 'visible').promise).rejects.toThrow('wait');
        await expect(runtime.acquireRendition({ ...request, fileId: 2 }, 'visible').promise).rejects.toMatchObject({ code: 'rate_limited' });
        expect(mocks.fetch).toHaveBeenCalledTimes(1);
        runtime.teardownRenditions();
    });

    it('preserves flood waits across memory-pressure session resets', async () => {
        const runtime = await import('./runtime');
        mocks.fetch.mockRejectedValueOnce(new RenditionError('rate_limited', 'wait', 3_701_000));
        await expect(runtime.acquireRendition(request, 'visible').promise).rejects.toThrow('wait');
        mocks.callbacks.get('gallery_memory_pressure')!();
        await expect(runtime.acquireRendition({ ...request, fileId: 2 }, 'visible').promise).rejects.toMatchObject({ code: 'rate_limited' });
        expect(mocks.fetch).toHaveBeenCalledTimes(1);
        runtime.teardownRenditions();
    });

    it('renews an expired session once without losing active subscribers', async () => {
        const runtime = await import('./runtime');
        mocks.fetch.mockRejectedValueOnce(new RenditionError('session_revoked', 'expired'));
        const lease = runtime.acquireRendition(request, 'visible');
        expect((await lease.promise).width).toBe(32);
        expect(mocks.open).toHaveBeenCalledTimes(2);
        expect(mocks.fetch).toHaveBeenCalledTimes(2);
        runtime.teardownRenditions();
    });

    it('releases disposable resources on memory pressure', async () => {
        const runtime = await import('./runtime');
        await runtime.acquireRendition(request, 'visible').promise;
        mocks.callbacks.get('gallery_memory_pressure')!();
        expect(runtime.renditionStats()).toBeNull();
        runtime.teardownRenditions();
    });
});
