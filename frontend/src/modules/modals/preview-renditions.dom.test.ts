import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const mocks = vi.hoisted(() => ({
    acquire: vi.fn(),
    mobile: vi.fn(() => false),
    notify: vi.fn(),
    download: vi.fn(),
    external: vi.fn(),
    password: vi.fn(),
    release: vi.fn(),
    close: vi.fn(),
    openOriginal: vi.fn(),
    setGalleryActive: vi.fn(),
    reserveOriginal: vi.fn(),
    releaseOriginalBudget: vi.fn(),
    resetListener: undefined as undefined | (() => void),
    policyListener: undefined as undefined | ((policy: { backgrounded: boolean }) => void),
}));
vi.mock('../../api', () => ({ closeMedia: mocks.close, isMobilePlatform: mocks.mobile, onRuntimeEvent: () => () => {}, openExternalUrl: mocks.external, openOriginalImage: mocks.openOriginal, requireOperationSuccess: vi.fn(), useEncryptionPassword: mocks.password }));
vi.mock('../renditions/runtime', () => ({ acquireRendition: mocks.acquire, subscribeRenditionReset: (listener: () => void) => { mocks.resetListener = listener; return () => {}; } }));
vi.mock('../gallery-policy', () => ({
    acquireOriginalViewerBudget: mocks.reserveOriginal,
    subscribeGalleryPolicy: (listener: (policy: { backgrounded: boolean }) => void) => { mocks.policyListener = listener; return () => {}; },
}));
vi.mock('../../ui/gallery/gallery-controller', () => ({ setActive: mocks.setGalleryActive }));
vi.mock('../notifications', () => ({ notify: mocks.notify }));
vi.mock('../encryption', () => ({ loadEncryptionStatus: vi.fn() }));
vi.mock('../transfers', () => ({ enqueueDownload: mocks.download }));
vi.mock('./preview-info', () => ({ renderImageInfoHTML: vi.fn(() => '') }));
vi.mock('./preview-transition', () => ({ capturePreviewTransitionSource: () => null, createPreviewTransitionController: () => ({ cancel: vi.fn(), isRunning: () => false, finishOpen: () => false, beginOpen: () => false, playClose: vi.fn() }) }));
const item = { type: 'file' as const, id: 1, name: 'Photo.jpg', channel_id: 10, content_revision: 2 };

function pointer(element: HTMLElement, type: string, x: number, y: number, pointerId = 1): void {
    element.dispatchEvent(new PointerEvent(type, {
        pointerId, pointerType: 'touch', clientX: x, clientY: y, bubbles: true, cancelable: true,
    }));
}

beforeEach(() => {
    vi.resetModules();
    vi.clearAllMocks();
    mocks.mobile.mockReturnValue(false);
    mocks.resetListener = undefined;
    mocks.policyListener = undefined;
    mocks.reserveOriginal.mockImplementation(() => mocks.releaseOriginalBudget);
    mocks.acquire.mockImplementation(() => ({ promise: Promise.resolve({ url: 'blob:photo', width: 1200, height: 800 }), release: mocks.release }));
    mocks.openOriginal.mockResolvedValue({ token: 'original-1', url: 'http://127.0.0.1/media/original-1', kind: 'image' });
    Object.defineProperty(HTMLImageElement.prototype, 'decode', { configurable: true, value: vi.fn(async () => {}) });
    document.body.innerHTML = '<div id="preview-modal" class="modal-overlay" style="display:none"><div id="preview-shell"><div id="preview-stage"><img id="preview-thumbnail"><img id="preview-image"><div id="preview-loading"><div id="preview-loading-fill"></div></div><div id="preview-error"></div><div id="preview-locked" style="display:none"><input id="preview-locked-input" type="password"><button id="preview-locked-eye"></button><button id="preview-locked-unlock"></button><div id="preview-locked-error"></div><div id="preview-locked-hint"><span id="preview-locked-hint-text"></span></div></div></div><span id="preview-filename"></span><button id="preview-download"></button><button id="preview-close"></button><button id="preview-prev"></button><button id="preview-next"></button><span id="preview-counter"></span><button id="preview-info-btn"></button><aside id="preview-info"><button id="preview-info-close"></button><div id="preview-info-body"></div></aside></div></div>';
});

afterEach(async () => {
    (await import('./preview')).teardownPreviewModal();
    vi.useRealTimers();
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
        expect(mocks.reserveOriginal).toHaveBeenCalledTimes(1);
        expect(mocks.setGalleryActive).toHaveBeenCalledWith(false);
        preview.closePreviewModal();
        expect(mocks.release).toHaveBeenCalledTimes(1);
        expect(mocks.close).toHaveBeenCalledWith('original-1');
        expect(mocks.releaseOriginalBudget).toHaveBeenCalledTimes(1);
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
        expect(mocks.releaseOriginalBudget).toHaveBeenCalledTimes(1);
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
        expect(mocks.releaseOriginalBudget).toHaveBeenCalledTimes(1);
        preview.closePreviewModal();
    });

    it('shows the unlock form when Wails wraps an encrypted image error', async () => {
        mocks.openOriginal.mockRejectedValue(new Error(
            'Binding call failed: Bound method returned an error: media: encryption key is unavailable: encryption password required',
        ));
        mocks.acquire.mockReturnValue({ promise: new Promise(() => {}), release: mocks.release });
        const preview = await import('./preview');

        preview.activatePreviewModal();
        await preview.openPreviewList([{ ...item, encrypted: true }], 0);

        expect(document.getElementById('preview-locked')?.style.display).toBe('flex');
        expect(document.getElementById('preview-error')?.textContent).toBe('');
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
        expect(mocks.releaseOriginalBudget).toHaveBeenCalledTimes(2);
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
        expect(mocks.releaseOriginalBudget).toHaveBeenCalledTimes(1);
        resolve({ token: 'late', url: 'http://127.0.0.1/media/late', kind: 'image' });
        await opening;
        expect(document.querySelector('#preview-image')?.getAttribute('src')).toBeNull();
    });
    it('does not reopen an original when unlocking finishes after closing', async () => {
        let resolvePassword!: () => void;
        mocks.password.mockReturnValue(new Promise<void>(resolve => { resolvePassword = resolve; }));
        const preview = await import('./preview');
        const { state } = await import('../../state');
        state.encryption.passwordRemembered = false;
        preview.activatePreviewModal();
        await preview.openPreviewList([{ ...item, encrypted: true }], 0);
        const input = document.getElementById('preview-locked-input') as HTMLInputElement;
        input.value = 'test-password';
        document.getElementById('preview-locked-unlock')!.click();
        expect(mocks.password).toHaveBeenCalledOnce();
        preview.closePreviewModal();
        state.encryption.passwordRemembered = true;
        resolvePassword();
        await vi.waitFor(() => expect(input.disabled).toBe(false));
        expect(mocks.openOriginal).not.toHaveBeenCalled();
        expect(mocks.acquire).not.toHaveBeenCalled();
        expect(document.getElementById('preview-modal')!.style.display).toBe('none');
    });

    it.each([
        ['plain rejection', ' network unavailable ', 'network unavailable'],
        ['structured rejection', { message: ' no image available ' }, 'no image available'],
        ['unknown rejection', null, 'Download failed'],
    ])('shows a useful error without a thumbnail for %s', async (_name, failure, message) => {
        mocks.acquire.mockReturnValue({ promise: Promise.reject(new Error('missing thumbnail')), release: mocks.release });
        mocks.openOriginal.mockRejectedValue(failure);
        const preview = await import('./preview');
        preview.activatePreviewModal();
        await preview.openPreviewList([item], 0);
        expect(document.getElementById('preview-error')!.textContent).toBe(message);
        expect(document.getElementById('preview-image')!.hasAttribute('src')).toBe(false);
        expect(mocks.releaseOriginalBudget).toHaveBeenCalledOnce();
        document.getElementById('preview-download')!.click();
        expect(mocks.download).toHaveBeenCalledWith(1, 'Photo.jpg', 0);
    });

    it('releases a decoded-original failure and retains its download action', async () => {
        const preview = await import('./preview');
        preview.activatePreviewModal();
        await preview.openPreviewList([{ ...item, size: 123 }], 0);
        document.getElementById('preview-image')!.dispatchEvent(new Event('error'));
        expect(mocks.close).toHaveBeenCalledExactlyOnceWith('original-1');
        expect(mocks.releaseOriginalBudget).toHaveBeenCalledOnce();
        expect(document.getElementById('preview-error')!.textContent).toBe('Not a supported image');
        document.getElementById('preview-download')!.click();
        expect(mocks.download).toHaveBeenCalledWith(1, 'Photo.jpg', 123);
        preview.closePreviewModal();
        expect(mocks.close).toHaveBeenCalledOnce();
    });

    it('keeps navigation usable after a neighbor lookup fails', async () => {
        const preview = await import('./preview');
        const source = { getNeighbor: vi.fn().mockRejectedValueOnce(new Error('offline')).mockResolvedValueOnce({ ...item, id: 2 }) };
        preview.activatePreviewModal();
        await preview.openPreviewSource(source, item);
        const next = document.getElementById('preview-next') as HTMLButtonElement;
        next.click();
        await vi.waitFor(() => expect(mocks.notify).toHaveBeenCalledWith(expect.objectContaining({ title: 'Could not load the next photo. Try again.' })));
        expect(next.disabled).toBe(false);
        expect(mocks.close).not.toHaveBeenCalled();
        next.click();
        await vi.waitFor(() => expect(mocks.openOriginal).toHaveBeenCalledWith(2, 2));
    });

    it('discards pending neighbors after a drive reset without reopening a capability', async () => {
        let resolve!: (value: typeof item) => void;
        const preview = await import('./preview');
        const source = { getNeighbor: vi.fn(() => new Promise<typeof item>(yes => { resolve = yes; })) };
        preview.activatePreviewModal();
        await preview.openPreviewSource(source, item);
        document.getElementById('preview-next')!.click();
        expect((document.getElementById('preview-next') as HTMLButtonElement).disabled).toBe(true);
        mocks.resetListener?.();
        resolve({ ...item, id: 2 });
        await Promise.resolve();
        await Promise.resolve();
        expect(mocks.openOriginal).toHaveBeenCalledOnce();
        expect(document.getElementById('preview-modal')!.style.display).toBe('none');
        expect(mocks.close).toHaveBeenCalledExactlyOnceWith('original-1');
    });

    it('releases pending original results without disturbing the newer photo', async () => {
        let reject!: (error: Error) => void;
        mocks.openOriginal.mockReturnValueOnce(new Promise((_resolve, no) => { reject = no; }));
        const preview = await import('./preview');
        preview.activatePreviewModal();
        const oldOpen = preview.openPreviewList([item], 0);
        await preview.openPreviewList([{ ...item, id: 2, name: 'New.jpg' }], 0);
        reject(new Error('old request failed'));
        await oldOpen;
        expect(document.getElementById('preview-filename')!.textContent).toBe('New.jpg');
        expect(document.getElementById('preview-error')!.textContent).toBe('');
        expect(mocks.releaseOriginalBudget).toHaveBeenCalledTimes(1);
    });

    it('releases sessions when its host is detached and removes old controls', async () => {
        const preview = await import('./preview');
        preview.activatePreviewModal();
        await preview.openPreviewList([item], 0);
        const host = document.getElementById('preview-modal')!;
        const download = document.getElementById('preview-download')!;
        host.remove();
        await vi.waitFor(() => expect(mocks.close).toHaveBeenCalledWith('original-1'));
        download.click();
        expect(mocks.download).not.toHaveBeenCalled();
        expect(mocks.release).toHaveBeenCalledOnce();
        expect(mocks.releaseOriginalBudget).toHaveBeenCalledOnce();
    });

    it('updates readiness, info focus and map actions from the active image', async () => {
        const preview = await import('./preview');
        preview.activatePreviewModal();
        await preview.openPreviewList([item], 0);
        const image = document.getElementById('preview-image')!;
        image.dispatchEvent(new Event('load'));
        expect(document.getElementById('preview-modal')!.classList.contains('is-original-ready')).toBe(true);
        expect(document.getElementById('preview-loading')!.style.display).toBe('none');
        window.dispatchEvent(new KeyboardEvent('keydown', { key: 'i', bubbles: true }));
        expect(document.getElementById('preview-info')!.getAttribute('aria-hidden')).toBe('false');
        const map = document.createElement('button');
        map.dataset.mapUrl = 'https://maps.example.test/place';
        document.getElementById('preview-info-body')!.append(map);
        map.click();
        expect(mocks.external).toHaveBeenCalledWith('https://maps.example.test/place');
        map.focus();
        document.getElementById('preview-info-close')!.click();
        expect(document.activeElement).toBe(document.getElementById('preview-info-btn'));
        expect(document.getElementById('preview-info')!.hasAttribute('inert')).toBe(true);
    });

    it('zooms a loaded image, resets on double click and closes on a desktop backdrop', async () => {
        const preview = await import('./preview');
        preview.activatePreviewModal();
        await preview.openPreviewList([item], 0);
        const image = document.getElementById('preview-image')!;
        const stage = document.getElementById('preview-stage')!;
        const wheel = new WheelEvent('wheel', { deltaY: -10, cancelable: true });
        stage.dispatchEvent(wheel);
        expect(wheel.defaultPrevented).toBe(true);
        expect(image.style.transform).toContain('scale(1.18)');
        image.dispatchEvent(new MouseEvent('dblclick', { cancelable: true }));
        expect(image.style.transform).toBe('');
        image.dispatchEvent(new MouseEvent('dblclick', { cancelable: true }));
        expect(image.style.transform).toContain('scale(2.5)');
        stage.click();
        expect(document.getElementById('preview-modal')!.style.display).toBe('none');
    });

    it('keeps focused close chrome visible and cancels the hide timer on teardown', async () => {
        vi.useFakeTimers();
        const preview = await import('./preview');
        preview.activatePreviewModal();
        await preview.openPreviewList([item], 0);
        const modal = document.getElementById('preview-modal')!;
        const close = document.getElementById('preview-close')!;
        close.focus();
        vi.advanceTimersByTime(2000);
        expect(modal.classList.contains('is-chrome-visible')).toBe(true);
        document.getElementById('preview-download')!.focus();
        modal.dispatchEvent(new PointerEvent('pointermove', { pointerType: 'mouse' }));
        vi.advanceTimersByTime(1600);
        expect(modal.classList.contains('is-chrome-visible')).toBe(false);
        modal.dispatchEvent(new PointerEvent('pointermove', { pointerType: 'mouse' }));
        expect(modal.classList.contains('is-chrome-visible')).toBe(true);
        preview.teardownPreviewModal();
        vi.advanceTimersByTime(2000);
        expect(modal.style.display).toBe('none');
    });

    it('handles mobile taps and pinch without the synthesized double-click zooming again', async () => {
        vi.useFakeTimers();
        mocks.mobile.mockReturnValue(true);
        const preview = await import('./preview');
        preview.activatePreviewModal();
        await preview.openPreviewList([item], 0);
        const modal = document.getElementById('preview-modal')!;
        const stage = document.getElementById('preview-stage')!;
        const image = document.getElementById('preview-image')!;
        pointer(stage, 'pointerdown', 100, 100);
        pointer(stage, 'pointerup', 100, 100);
        vi.advanceTimersByTime(300);
        expect(modal.classList.contains('is-chrome-visible')).toBe(false);
        stage.click();
        expect(modal.style.display).toBe('flex');
        modal.dispatchEvent(new PointerEvent('pointermove', { pointerType: 'touch' }));
        expect(modal.classList.contains('is-chrome-visible')).toBe(false);
        pointer(stage, 'pointerdown', 100, 100);
        pointer(stage, 'pointerup', 100, 100);
        pointer(stage, 'pointerdown', 100, 100);
        pointer(stage, 'pointerup', 100, 100);
        expect(image.style.transform).toContain('scale(2.5)');
        image.dispatchEvent(new MouseEvent('dblclick', { bubbles: true }));
        expect(image.style.transform).toContain('scale(2.5)');
        pointer(stage, 'pointerdown', 100, 100);
        pointer(stage, 'pointerup', 100, 100);
        pointer(stage, 'pointerdown', 100, 100);
        pointer(stage, 'pointerup', 100, 100);
        expect(image.style.transform).toBe('');
        pointer(stage, 'pointerdown', 100, 100, 1);
        pointer(stage, 'pointerdown', 200, 100, 2);
        pointer(stage, 'pointermove', 300, 100, 2);
        expect(image.style.transform).toContain('scale(2)');
        pointer(stage, 'pointerup', 100, 100, 1);
        pointer(stage, 'pointerup', 300, 100, 2);
    });

    it('navigates with horizontal swipes and dismisses with a downward swipe on mobile', async () => {
        mocks.mobile.mockReturnValue(true);
        const preview = await import('./preview');
        preview.activatePreviewModal();
        await preview.openPreviewList([item, { ...item, id: 2 }], 0);
        const stage = document.getElementById('preview-stage')!;
        const image = document.getElementById('preview-image')!;
        pointer(stage, 'pointerdown', 200, 200);
        pointer(stage, 'pointermove', 100, 200);
        expect(image.style.transform).toContain('translate3d(-100px');
        pointer(stage, 'pointerup', 100, 200);
        await vi.waitFor(() => expect(mocks.openOriginal).toHaveBeenCalledWith(2, 2));
        pointer(stage, 'pointerdown', 100, 200);
        pointer(stage, 'pointermove', 200, 200);
        pointer(stage, 'pointerup', 200, 200);
        await vi.waitFor(() => expect(mocks.openOriginal).toHaveBeenCalledTimes(3));
        expect(document.getElementById('preview-counter')!.textContent).toBe('1 / 2');
        pointer(stage, 'pointerdown', 100, 200);
        pointer(stage, 'pointermove', 100, 340);
        expect(document.getElementById('preview-modal')!.style.getPropertyValue('--preview-dismiss')).not.toBe('');
        pointer(stage, 'pointerup', 100, 340);
        expect(document.getElementById('preview-modal')!.style.display).toBe('none');
        expect(image.style.transform).toBe('');
    });

    it('keeps a newly selected photo when a previous inline unlock resolves', async () => {
        let resolvePassword!: () => void;
        mocks.password.mockReturnValue(new Promise<void>(resolve => { resolvePassword = resolve; }));
        const preview = await import('./preview');
        const { state } = await import('../../state');
        state.encryption.passwordRemembered = false;
        preview.activatePreviewModal();
        await preview.openPreviewList([{ ...item, encrypted: true }], 0);
        const input = document.getElementById('preview-locked-input') as HTMLInputElement;
        input.value = 'test-password';
        document.getElementById('preview-locked-eye')!.click();
        expect(input.type).toBe('text');
        input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true }));
        await preview.openPreviewList([{ ...item, id: 2, name: 'New.jpg' }], 0);
        state.encryption.passwordRemembered = true;
        resolvePassword();
        await vi.waitFor(() => expect(input.disabled).toBe(false));
        expect(mocks.openOriginal).toHaveBeenCalledExactlyOnceWith(2, 2);
        expect(document.getElementById('preview-filename')!.textContent).toBe('New.jpg');
    });

});
