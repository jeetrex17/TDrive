import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({ acquire: vi.fn(), release: vi.fn(), allowPrefetch: false }));
vi.mock('../../api', () => ({ hasOperationErrorCode: () => false, isMobilePlatform: () => false, onRuntimeEvent: () => () => {}, openExternalUrl: vi.fn(), useEncryptionPassword: vi.fn() }));
vi.mock('../renditions/runtime', () => ({ acquireRendition: mocks.acquire, subscribeRenditionReset: () => () => {} }));
vi.mock('../gallery-policy', () => ({ getGalleryPolicy: () => ({ allowPrefetch: mocks.allowPrefetch }), subscribeGalleryPolicy: () => () => {} }));
vi.mock('../notifications', () => ({ notify: vi.fn() }));
vi.mock('../encryption', () => ({ loadEncryptionStatus: vi.fn() }));
vi.mock('../transfers', () => ({ enqueueDownload: vi.fn() }));
vi.mock('./preview-info', () => ({ renderImageInfoHTML: vi.fn(() => '') }));
vi.mock('./preview-transition', () => ({ capturePreviewTransitionSource: () => null, createPreviewTransitionController: () => ({ cancel: vi.fn(), isRunning: () => false, finishOpen: () => false, beginOpen: () => false, playClose: vi.fn() }) }));
const item = { type: 'file' as const, id: 1, name: 'Photo.jpg', channel_id: 10, content_revision: 2 };

beforeEach(() => {
    vi.resetModules();
    vi.clearAllMocks();
    mocks.allowPrefetch = false;
    mocks.acquire.mockImplementation(() => ({ promise: Promise.resolve({ url: 'blob:photo', width: 1200, height: 800 }), release: mocks.release }));
    Object.defineProperty(HTMLImageElement.prototype, 'decode', { configurable: true, value: vi.fn(async () => {}) });
    document.body.innerHTML = '<div id="preview-modal" class="modal-overlay" style="display:none"><div id="preview-shell"><div id="preview-stage"><img id="preview-image"><div id="preview-loading"><div id="preview-loading-fill"></div></div><div id="preview-error"></div></div><span id="preview-filename"></span><button id="preview-close"></button><button id="preview-prev"></button><button id="preview-next"></button><span id="preview-counter"></span></div></div>';
});

describe('bounded photo viewer', () => {
    it('loads only a screen preview and releases its lease on close', async () => {
        const preview = await import('./preview');
        preview.activatePreviewModal();
        await preview.openPreviewList([item, { ...item, id: 2 }], 0);
        expect(mocks.acquire).toHaveBeenCalledTimes(1);
        expect(mocks.acquire).toHaveBeenCalledWith(expect.objectContaining({ fileId: 1, revision: 2, kind: 'preview' }), 'viewer');
        preview.closePreviewModal();
        expect(mocks.release).toHaveBeenCalledTimes(1);
        expect(document.querySelector('#preview-image')?.getAttribute('src')).toBeNull();
    });

    it('prefetches at most one next preview when policy permits it', async () => {
        mocks.allowPrefetch = true;
        const preview = await import('./preview');
        preview.activatePreviewModal();
        await preview.openPreviewList(Array.from({ length: 100 }, (_, index) => ({ ...item, id: index + 1 })), 5);
        await vi.waitFor(() => expect(mocks.acquire).toHaveBeenCalledTimes(2));
        expect(mocks.acquire.mock.calls[1]).toEqual([expect.objectContaining({ fileId: 7, kind: 'preview' }), 'prefetch']);
        preview.closePreviewModal();
    });

    it('navigates through an asynchronous source without retaining the library', async () => {
        const preview = await import('./preview');
        const source = { getNeighbor: vi.fn(async () => ({ ...item, id: 2 })), getPosition: (value: typeof item) => ({ index: value.id - 1, total: 100_000 }) };
        preview.activatePreviewModal();
        await preview.openPreviewSource(source, item);
        document.getElementById('preview-next')!.click();
        await vi.waitFor(() => expect(mocks.acquire).toHaveBeenCalledTimes(2));
        expect(source.getNeighbor).toHaveBeenCalledWith(item, 1);
        expect(document.getElementById('preview-counter')!.textContent).toBe('2 / 100000');
        preview.closePreviewModal();
    });

    it('keeps a leased grid thumbnail visible while its screen preview loads', async () => {
        let finish!: (value: { url: string; width: number; height: number }) => void;
        const full = new Promise<{ url: string; width: number; height: number }>(resolve => { finish = resolve; });
        const thumbnailRelease = vi.fn();
        mocks.acquire.mockImplementation(request => request.kind === 'thumbnail'
            ? { promise: Promise.resolve({ url: 'blob:thumb', width: 32, height: 32 }), release: thumbnailRelease }
            : { promise: full, release: mocks.release });
        const preview = await import('./preview');
        preview.activatePreviewModal();
        const opening = preview.openPreviewList([{ ...item, thumbUrl: 'blob:thumb' }], 0);
        await vi.waitFor(() => expect(document.querySelector('#preview-image')?.getAttribute('src')).toBe('blob:thumb'));
        finish({ url: 'blob:screen', width: 1200, height: 800 });
        await opening;
        expect(thumbnailRelease).toHaveBeenCalledOnce();
        expect(document.querySelector('#preview-image')?.getAttribute('src')).toBe('blob:screen');
        preview.closePreviewModal();
    });

    it('lets another navigation cancel a slow image after metadata resolves', async () => {
        mocks.acquire.mockImplementation(request => ({
            promise: request.fileId === 2 ? new Promise(() => {}) : Promise.resolve({ url: 'blob:photo', width: 1200, height: 800 }),
            release: mocks.release,
        }));
        const preview = await import('./preview');
        preview.activatePreviewModal();
        await preview.openPreviewList([item, { ...item, id: 2 }, { ...item, id: 3 }], 0);
        document.getElementById('preview-next')!.click();
        await vi.waitFor(() => expect(mocks.acquire).toHaveBeenCalledTimes(2));
        document.getElementById('preview-next')!.click();
        await vi.waitFor(() => expect(mocks.acquire).toHaveBeenCalledTimes(3));
        expect(document.getElementById('preview-counter')!.textContent).toBe('3 / 3');
        preview.closePreviewModal();
    });

    it('ignores a photo that finishes after the viewer closes', async () => {
        let resolve!: (value: { url: string; width: number; height: number }) => void;
        mocks.acquire.mockReturnValue({ promise: new Promise(yes => { resolve = yes; }), release: mocks.release });
        const preview = await import('./preview');
        preview.activatePreviewModal();
        const opening = preview.openPreviewList([item], 0);
        preview.closePreviewModal();
        resolve({ url: 'blob:late', width: 10, height: 10 });
        await opening;
        expect(document.querySelector('#preview-image')?.getAttribute('src')).toBeNull();
    });
});
