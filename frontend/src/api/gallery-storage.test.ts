import { describe, expect, it, vi } from 'vitest';
const calls = vi.hoisted(() => ({ GetGalleryStorage: vi.fn(), ClearGalleryCache: vi.fn() }));
vi.mock('../../bindings/TDrive/app', () => calls);
import { getGalleryStorage, clearGalleryCache } from './gallery-storage';

describe('gallery storage API', () => {
    it('normalizes usage and clear responses', async () => {
        const raw = { cache_bytes: 12, cache_limit: 1024, catalog_bytes: 99 };
        calls.GetGalleryStorage.mockResolvedValue(raw);
        calls.ClearGalleryCache.mockResolvedValue({ ...raw, cache_bytes: 0 });
        expect(await getGalleryStorage()).toEqual({ cacheBytes: 12, cacheLimit: 1024, catalogBytes: 99 });
        expect(await clearGalleryCache()).toEqual({ cacheBytes: 0, cacheLimit: 1024, catalogBytes: 99 });
    });
    it('rejects misleading negative usage', async () => {
        calls.GetGalleryStorage.mockResolvedValue({ cache_bytes: -1, cache_limit: 1, catalog_bytes: 1 });
        await expect(getGalleryStorage()).rejects.toThrow('Invalid storage');
    });
});
