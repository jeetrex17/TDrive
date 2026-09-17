import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import GalleryPreparation from './GalleryPreparation.svelte';

const api = vi.hoisted(() => ({ status: vi.fn(), start: vi.fn(), stop: vi.fn() }));
vi.mock('../../api/gallery', () => ({ getGalleryPreparation: api.status, startGalleryPreparation: api.start, stopGalleryPreparation: api.stop, normalizeGalleryPreparation: (value: unknown) => value }));
vi.mock('../../api', () => ({ isMobilePlatform: () => false, onRuntimeEvent: () => () => {} }));
vi.mock('../../modules/modals/encryption-password', () => ({ callWithPasswordRetry: (call: () => unknown) => call() }));
vi.mock('./gallery-controller', () => ({ rearmMissing: vi.fn() }));

let host: HTMLElement;
let app: Record<string, unknown>;
async function settle() { flushSync(); await Promise.resolve(); await Promise.resolve(); flushSync(); }
beforeEach(() => {
    api.status.mockResolvedValue({ running: false, channelId: 1, completed: 0, total: 10, bytesTotal: 10_000_000, bytesDone: 0, error: '' });
    api.start.mockResolvedValue({ ok: true });
    api.stop.mockResolvedValue({ ok: true });
    api.start.mockClear(); api.stop.mockClear();
    host = document.createElement('div'); document.body.append(host);
    app = mount(GalleryPreparation, { target: host, props: { channelId: 1 } });
});
afterEach(async () => { await unmount(app); host.remove(); });

describe('explicit preview preparation', () => {
    it('does not download until the user reviews the transfer and starts', async () => {
        await settle();
        expect(api.start).not.toHaveBeenCalled();
        host.querySelector<HTMLButtonElement>('.gallery-prepare-action')!.click();
        await settle();
        expect(host.querySelector('[role="dialog"]')?.textContent).toContain('10 photos');
        expect(host.querySelector('[role="dialog"]')?.textContent).toContain('download');
        expect(api.start).not.toHaveBeenCalled();
        host.querySelector<HTMLButtonElement>('#gallery-prepare-confirm')!.click();
        await settle();
        expect(api.start).toHaveBeenCalledWith(1);
    });

    it('lets a running preparation pause', async () => {
        api.status.mockResolvedValue({ running: true, channelId: 1, completed: 2, total: 10, bytesTotal: 10_000_000, bytesDone: 2_000_000, error: '' });
        await unmount(app);
        app = mount(GalleryPreparation, { target: host, props: { channelId: 1 } });
        await settle();
        host.querySelector<HTMLButtonElement>('.gallery-prepare-pause')!.click();
        await settle();
        expect(api.stop).toHaveBeenCalledOnce();
    });
});
