import { beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
    acquire: vi.fn(),
    release: vi.fn(),
    close: vi.fn(),
    openOriginal: vi.fn(),
    setGalleryActive: vi.fn(),
    resetListener: undefined as undefined | (() => void),
    policyListener: undefined as undefined | ((policy: { backgrounded: boolean }) => void),
}));
vi.mock('../../api', () => ({ closeMedia: mocks.close, hasOperationErrorCode: () => false, isMobilePlatform: () => false, onRuntimeEvent: () => () => {}, openExternalUrl: vi.fn(), openOriginalImage: mocks.openOriginal, useEncryptionPassword: vi.fn() }));
vi.mock('../renditions/runtime', () => ({ acquireRendition: mocks.acquire, subscribeRenditionReset: (listener: () => void) => { mocks.resetListener = listener; return () => {}; } }));
vi.mock('../gallery-policy', () => ({ subscribeGalleryPolicy: (listener: (policy: { backgrounded: boolean }) => void) => { mocks.policyListener = listener; return () => {}; } }));
vi.mock('../../ui/gallery/gallery-controller', () => ({ setActive: mocks.setGalleryActive }));
vi.mock('../notifications', () => ({ notify: vi.fn() }));
vi.mock('../encryption', () => ({ loadEncryptionStatus: vi.fn() }));
vi.mock('../transfers', () => ({ enqueueDownload: vi.fn() }));
vi.mock('./preview-info', () => ({ renderImageInfoHTML: vi.fn(() => '') }));
vi.mock('./preview-transition', () => ({ capturePreviewTransitionSource: () => null, createPreviewTransitionController: () => ({ cancel: vi.fn(), isRunning: () => false, finishOpen: () => false, beginOpen: () => false, playClose: vi.fn() }) }));
const item = { type: 'file' as const, id: 1, name: 'Photo.jpg', channel_id: 10, content_revision: 2 };

beforeEach(() => {
    vi.resetModules();
    vi.clearAllMocks();
    mocks.resetListener = undefined;
    mocks.policyListener = undefined;
    mocks.acquire.mockImplementation(() => ({ promise: Promise.resolve({ url: 'blob:photo', width: 1200, height: 800 }), release: mocks.release }));
    mocks.openOriginal.mockResolvedValue({ token: 'original-1', url: 'http://127.0.0.1/media/original-1', kind: 'image' });
    Object.defineProperty(HTMLImageElement.prototype, 'decode', { configurable: true, value: vi.fn(async () => {}) });
    document.body.innerHTML = '<div id="preview-modal" class="modal-overlay" style="display:none"><div id="preview-shell"><div id="preview-stage"><img id="preview-thumbnail"><img id="preview-image"><div id="preview-loading"><div id="preview-loading-fill"></div></div><div id="preview-error"></div></div><span id="preview-filename"></span><button id="preview-download"></button><button id="preview-close"></button><button id="preview-prev"></button><button id="preview-next"></button><span id="preview-counter"></span></div></div>';
});

describe('bounded photo viewer', () => {
    it('offers direct viewing only for backend-validated raster formats', async () => {
        const preview = await import('./preview');
        expect(preview.isPreviewableImage('photo.JPEG')).toBe(true);
        expect(preview.isPreviewableImage('photo.webp')).toBe(true);
        expect(preview.isPreviewableImage('vector.svg')).toBe(false);
        expect(preview.isPreviewableImage('photo.jpg.exe')).toBe(false);
    });

    it('pins only an immutable thumbnail and opens one original session after the explicit viewer click', async () => {
        const preview = await import('./preview');
        preview.activatePreviewModal();
        await preview.openPreviewList([item, { ...item, id: 2 }], 0);
        expect(mocks.acquire).toHaveBeenCalledTimes(1);
        expect(mocks.acquire).toHaveBeenCalledWith(expect.objectContaining({ fileId: 1, revision: 2, kind: 'thumbnail' }), 'viewer');
        expect(mocks.openOriginal).toHaveBeenCalledExactlyOnceWith(1, 2);
        expect(mocks.setGalleryActive).toHaveBeenCalledWith(false);
        preview.closePreviewModal();
        expect(mocks.release).toHaveBeenCalledTimes(1);
        expect(mocks.close).toHaveBeenCalledWith('original-1');
        expect(mocks.setGalleryActive).toHaveBeenLastCalledWith(true);
        expect(document.querySelector('#preview-image')?.getAttribute('src')).toBeNull();
    });

    it('never prefetches neighbor originals', async () => {
        const preview = await import('./preview');
        preview.activatePreviewModal();
        await preview.openPreviewList(Array.from({ length: 100 }, (_, index) => ({ ...item, id: index + 1 })), 5);
        expect(mocks.acquire).toHaveBeenCalledTimes(1);
        expect(mocks.openOriginal).toHaveBeenCalledTimes(1);
        preview.closePreviewModal();
    });

    it('closes the original capability when rendition state resets', async () => {
        const preview = await import('./preview');
        preview.activatePreviewModal();
        await preview.openPreviewList([item], 0);

        mocks.resetListener?.();

        expect(mocks.close).toHaveBeenCalledWith('original-1');
        expect(document.getElementById('preview-modal')?.style.display).toBe('none');
    });

    it('closes the original capability when the app enters the background', async () => {
        const preview = await import('./preview');
        preview.activatePreviewModal();
        await preview.openPreviewList([item], 0);

        mocks.policyListener?.({ backgrounded: true });

        expect(mocks.close).toHaveBeenCalledWith('original-1');
        expect(document.getElementById('preview-modal')?.style.display).toBe('none');
    });

    it('navigates through an asynchronous source without retaining the library', async () => {
        const preview = await import('./preview');
        const source = { getNeighbor: vi.fn(async () => ({ ...item, id: 2 })), getPosition: (value: typeof item) => ({ index: value.id - 1, total: 100_000 }) };
        preview.activatePreviewModal();
        await preview.openPreviewSource(source, item);
        document.getElementById('preview-next')!.click();
        await vi.waitFor(() => expect(mocks.acquire).toHaveBeenCalledTimes(2));
        expect(source.getNeighbor).toHaveBeenCalledWith(item, 1);
        expect(mocks.openOriginal.mock.calls).toEqual([[1, 2], [2, 2]]);
        expect(mocks.close).toHaveBeenCalledWith('original-1');
        expect(document.getElementById('preview-counter')!.textContent).toBe('2 / 100000');
        preview.closePreviewModal();
    });

    it('keeps a reacquired thumbnail visible while the original stream is opening', async () => {
        let finish!: (value: { token: string; url: string; kind: string }) => void;
        const original = new Promise<{ token: string; url: string; kind: string }>(resolve => { finish = resolve; });
        const thumbnailRelease = vi.fn();
        mocks.acquire.mockReturnValue({ promise: Promise.resolve({ url: 'blob:thumb', width: 32, height: 32 }), release: thumbnailRelease });
        mocks.openOriginal.mockReturnValue(original);
        const preview = await import('./preview');
        preview.activatePreviewModal();
        const opening = preview.openPreviewList([{ ...item, thumbUrl: 'blob:thumb' }], 0);
        await vi.waitFor(() => expect(document.querySelector('#preview-thumbnail')?.getAttribute('src')).toBe('blob:thumb'));
        finish({ token: 'original-1', url: 'http://127.0.0.1/media/original-1', kind: 'image' });
        await opening;
        expect(thumbnailRelease).not.toHaveBeenCalled();
        expect(document.querySelector('#preview-image')?.getAttribute('src')).toBe('http://127.0.0.1/media/original-1');
        preview.closePreviewModal();
    });

    it('keeps Download available when direct original display is rejected', async () => {
        mocks.openOriginal.mockRejectedValue(new Error('animated image is unavailable for direct display'));
        mocks.acquire.mockReturnValue({ promise: Promise.resolve({ url: 'blob:thumb', width: 32, height: 32 }), release: mocks.release });
        const preview = await import('./preview');
        preview.activatePreviewModal();
        await preview.openPreviewList([item], 0);
        await vi.waitFor(() => expect(document.querySelector('#preview-thumbnail')?.getAttribute('src')).toBe('blob:thumb'));
        expect(document.getElementById('preview-download')?.hasAttribute('hidden')).toBe(false);
        expect(document.getElementById('preview-error')?.textContent).toContain('animated image');
        preview.closePreviewModal();
    });

    it('lets another navigation cancel a slow original session after metadata resolves', async () => {
        mocks.openOriginal.mockImplementation((fileId: number) => fileId === 2
            ? new Promise(() => {})
            : Promise.resolve({ token: `original-${fileId}`, url: `http://127.0.0.1/media/original-${fileId}`, kind: 'image' }));
        const preview = await import('./preview');
        preview.activatePreviewModal();
        await preview.openPreviewList([item, { ...item, id: 2 }, { ...item, id: 3 }], 0);
        document.getElementById('preview-next')!.click();
        await vi.waitFor(() => expect(mocks.acquire).toHaveBeenCalledTimes(2));
        document.getElementById('preview-next')!.click();
        await vi.waitFor(() => expect(mocks.acquire).toHaveBeenCalledTimes(3));
        expect(mocks.close).toHaveBeenCalledWith('original-1');
        expect(document.getElementById('preview-counter')!.textContent).toBe('3 / 3');
        preview.closePreviewModal();
    });

    it('ignores a photo that finishes after the viewer closes', async () => {
        let resolve!: (value: { token: string; url: string; kind: string }) => void;
        mocks.openOriginal.mockReturnValue(new Promise(yes => { resolve = yes; }));
        const preview = await import('./preview');
        preview.activatePreviewModal();
        const opening = preview.openPreviewList([item], 0);
        preview.closePreviewModal();
        resolve({ token: 'late', url: 'http://127.0.0.1/media/late', kind: 'image' });
        await opening;
        expect(document.querySelector('#preview-image')?.getAttribute('src')).toBeNull();
    });
});
