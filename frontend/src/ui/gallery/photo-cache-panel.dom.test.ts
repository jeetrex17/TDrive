import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';

const mocks = vi.hoisted(() => ({ getGalleryStorage: vi.fn(), clearGalleryCache: vi.fn() }));
vi.mock('../../api/gallery-storage', () => mocks);
import PhotoCachePanel from './PhotoCachePanel.svelte';

let app: ReturnType<typeof mount> | undefined;
afterEach(async () => { if (app) await unmount(app); app = undefined; document.body.innerHTML = ''; vi.clearAllMocks(); });

describe('photo cache controls', () => {
    it('shows catalog separately and clears only through the cache API', async () => {
        mocks.getGalleryStorage.mockResolvedValue({ cacheBytes: 1024, cacheLimit: 268435456, catalogBytes: 2048 });
        mocks.clearGalleryCache.mockResolvedValue({ cacheBytes: 0, cacheLimit: 268435456, catalogBytes: 2048 });
        app = mount(PhotoCachePanel, { target: document.body });
        await vi.waitFor(() => expect(document.body.textContent).toContain('1 KB'));
        expect(document.body.textContent).toContain('Local catalog');
        document.querySelector<HTMLButtonElement>('[data-clear-photo-cache]')!.click();
        await vi.waitFor(() => expect(document.body.textContent).toContain('Photo cache cleared'));
        expect(mocks.clearGalleryCache).toHaveBeenCalledTimes(1);
        expect(document.body.textContent).toContain('2 KB');
    });

    it('keeps an actionable error when cleanup fails', async () => {
        mocks.getGalleryStorage.mockResolvedValue({ cacheBytes: 1024, cacheLimit: 268435456, catalogBytes: 2048 });
        mocks.clearGalleryCache.mockRejectedValue(new Error('disk busy'));
        app = mount(PhotoCachePanel, { target: document.body });
        await vi.waitFor(() => expect(document.querySelector<HTMLButtonElement>('[data-clear-photo-cache]')?.disabled).toBe(false));
        document.querySelector<HTMLButtonElement>('[data-clear-photo-cache]')!.click();
        flushSync();
        await vi.waitFor(() => expect(document.querySelector('[role="alert"]')?.textContent).toContain('Try again'));
        expect(document.querySelector<HTMLButtonElement>('[data-clear-photo-cache]')?.disabled).toBe(false);
    });
});
