import { afterEach, describe, expect, it, vi } from 'vitest';
import { mount, unmount } from 'svelte';

const mocks = vi.hoisted(() => ({ getGalleryStorage: vi.fn() }));
vi.mock('../../api/gallery-storage', () => mocks);
import PhotoCachePanel from './PhotoCachePanel.svelte';

let app: ReturnType<typeof mount> | undefined;
afterEach(async () => { if (app) await unmount(app); app = undefined; document.body.innerHTML = ''; vi.clearAllMocks(); });

describe('photo cache controls', () => {
    it('shows automatic cache management without a clear button', async () => {
        mocks.getGalleryStorage.mockResolvedValue({ cacheBytes: 1024, cacheLimit: 268435456, catalogBytes: 2048 });
        app = mount(PhotoCachePanel, { target: document.body });
        await vi.waitFor(() => expect(document.body.textContent).toContain('1 KB'));
        expect(document.body.textContent).toContain('Local catalog');
        expect(document.body.textContent).toContain('removed automatically');
        expect(document.querySelector('button')).toBeNull();
        expect(document.body.textContent).toContain('2 KB');
    });

    it('allows retrying a failed storage read', async () => {
        mocks.getGalleryStorage.mockRejectedValueOnce(new Error('unavailable'))
            .mockResolvedValue({ cacheBytes: 1024, cacheLimit: 268435456, catalogBytes: 2048 });
        app = mount(PhotoCachePanel, { target: document.body });
        await vi.waitFor(() => expect(document.querySelector('[role="alert"]')?.textContent).toContain('Try again'));
        document.querySelector<HTMLButtonElement>('button')!.click();
        await vi.waitFor(() => expect(document.body.textContent).toContain('1 KB'));
        expect(document.querySelector('[role="alert"]')).toBeNull();
    });
});
