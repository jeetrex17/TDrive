import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { get } from 'svelte/store';
import type { MediaTimeline } from '../../api/gallery';

const mocks = vi.hoisted(() => ({ timeline: vi.fn(), folderTimeline: vi.fn(), page: vi.fn(), folderPage: vi.fn(), folders: vi.fn(), locate: vi.fn(), preview: vi.fn(), mobile: false }));
vi.mock('../../api', () => ({ isMobilePlatform: () => mocks.mobile }));
vi.mock('../../api/gallery', () => ({ getMediaTimelineSummary: mocks.timeline, getMediaTimelineAnchors: mocks.timeline, getMediaFolderTimeline: mocks.folderTimeline, listMediaPage: mocks.page, listMediaFolderPage: mocks.folderPage, listMediaFolders: mocks.folders, locateMedia: mocks.locate }));
vi.mock('../../modules/search', () => ({ clearSearch: vi.fn() }));
vi.mock('../../modules/app-actions', () => ({ appActions: () => ({ refreshFiles: vi.fn(), triggerRefresh: vi.fn() }) }));
vi.mock('../../modules/file-list', () => ({ canOwnerActOnFile: () => true }));
vi.mock('../../modules/selection', () => ({ updateSelectionBar: vi.fn() }));
vi.mock('../file-list/touch', () => ({ bindLongPress: () => () => {}, bindPullToRefresh: () => () => {} }));
vi.mock('./gallery-controller', () => ({ beginRender: vi.fn(), cachedThumb: () => '', rearmLocked: vi.fn(), setActive: vi.fn(), setRoot: vi.fn(), teardown: vi.fn() }));
vi.mock('../../modules/modals/preview', () => ({ activatePreviewModal: vi.fn(), openPreviewSource: mocks.preview }));
import { state } from '../../state';
import { activateGallery, enterPhotos, exitPhotos, renderGallery, setPhotosMode, teardownGallery, toggleGallerySelection } from '../../modules/gallery';
import { albumsView, galleryView, photosMode } from './gallery-store';

const timeline = (channelId = 1, generation = '1'): MediaTimeline => ({
    channelId, generation, totalCount: 1000, pageSize: 128,
    buckets: [{ key: '2026-09', startIndex: 0, count: 1000, uploadTime: 1 }],
    anchors: Array.from({ length: 8 }, (_, page) => ({ startIndex: page * 128, cursor: String(page * 128) })),
});
let generation: string;
let host: HTMLElement;
beforeEach(() => {
    generation = '1';
    mocks.mobile = false;
    state.activeChannel = { id: 1 } as typeof state.activeChannel;
    state.virtualView = 'photos';
    state.selectedItems = new Map();
    mocks.timeline.mockReset().mockImplementation(async () => timeline());
    mocks.page.mockReset().mockImplementation(async (cursor: string) => ({ generation, startIndex: Number(cursor), nextCursor: '', items: Array.from({ length: Math.min(128, 1000 - Number(cursor)) }, (_, offset) => ({ msgId: Number(cursor) + offset + 1, name: 'photo.jpg', size: 12, parentId: '', uploadTime: 1, uploaderId: 1, encrypted: false, plaintextSize: 0, revision: 1, contentMsgId: 1, contentHash: '' })) }));
    mocks.preview.mockReset(); mocks.locate.mockReset();
    mocks.folders.mockReset().mockResolvedValue([]);
    mocks.folderTimeline.mockReset(); mocks.folderPage.mockReset();
    photosMode.set({ kind: 'timeline' });
    host = document.createElement('div'); host.id = 'gallery-view'; document.body.append(host);
    activateGallery();
});
afterEach(() => { teardownGallery(); host.remove(); galleryView.set({ status: 'loading' }); albumsView.set({ status: 'loading' }); photosMode.set({ kind: 'timeline' }); });

describe('gallery orchestration', () => {
    it('keeps an unchanged snapshot and its cached pages on refresh', async () => {
        await renderGallery();
        const original = get(galleryView);
        await renderGallery({ background: true });
        expect(get(galleryView)).toBe(original);
        expect(mocks.page).toHaveBeenCalledOnce();
    });

    it('shows empty/error states and ignores navigation-away completions', async () => {
        mocks.timeline.mockResolvedValue({ ...timeline(), totalCount: 0, buckets: [], anchors: [] });
        await renderGallery();
        expect(get(galleryView)).toEqual({ status: 'empty' });
        teardownGallery(); activateGallery();
        const log = vi.spyOn(console, 'error').mockImplementation(() => {});
        mocks.timeline.mockRejectedValue(new Error('database unavailable'));
        await renderGallery();
        expect(get(galleryView)).toEqual({ status: 'error' });
        log.mockRestore();
        mocks.timeline.mockResolvedValue(timeline());
        state.virtualView = null;
        await renderGallery();
        expect(get(galleryView)).toEqual({ status: 'loading' });
    });

    it('retains loaded photos when a background metadata refresh fails', async () => {
        await renderGallery();
        const original = get(galleryView);
        const log = vi.spyOn(console, 'error').mockImplementation(() => {});
        mocks.timeline.mockRejectedValue(new Error('offline'));
        await renderGallery({ background: true });
        expect(get(galleryView)).toBe(original);
        log.mockRestore();
    });

    it('retries one stale summary-to-anchor race and keeps the current view if epochs keep changing', async () => {
        await renderGallery();
        const original = get(galleryView);
        host.dataset.anchorIndex = '128';
        generation = '2';
        mocks.timeline.mockResolvedValue(timeline(1, '2'));
        mocks.locate.mockResolvedValue({ generation: '2', index: 128, cursor: '128' });
        const stale = new Error('gallery snapshot is stale');
        // The first call in each retry is the summary; the second is anchors.
        mocks.timeline
            .mockResolvedValueOnce(timeline(1, '2'))
            .mockRejectedValueOnce(stale)
            .mockResolvedValueOnce(timeline(1, '3'))
            .mockRejectedValueOnce(stale);
        const log = vi.spyOn(console, 'error').mockImplementation(() => {});
        await renderGallery({ background: true });
        expect(get(galleryView)).toBe(original);
        expect(mocks.timeline).toHaveBeenCalledTimes(5); // initial render + two bounded attempts
        log.mockRestore();
    });

    // A drive whose timeline and pages never agree on a generation used to
    // spawn two renders per failed one -- the source's own stale callback and
    // the catch -- each with a fresh source, so the retries multiplied without
    // limit: tens of thousands of timeline calls in seconds, and a page that
    // stopped answering. A persistent disagreement is now one bounded retry
    // and then the error state.
    it('gives up on a persistent generation mismatch after one retry', async () => {
        mocks.timeline.mockImplementation(async () => timeline(1, 'summary'));
        mocks.page.mockImplementation(async (cursor: string) => ({ generation: 'pages', startIndex: Number(cursor), nextCursor: '', items: Array.from({ length: 128 }, (_, offset) => ({ msgId: Number(cursor) + offset + 1, name: 'photo.jpg', size: 12, parentId: '', uploadTime: 1, uploaderId: 1, encrypted: false, plaintextSize: 0, revision: 1, contentMsgId: 1, contentHash: '' })) }));
        const log = vi.spyOn(console, 'error').mockImplementation(() => {});
        await renderGallery();
        // Let any stray re-render that the old code would have queued run out.
        await new Promise((resolve) => setTimeout(resolve, 50));
        expect(get(galleryView)).toEqual({ status: 'error' });
        expect(mocks.timeline.mock.calls.length).toBeLessThanOrEqual(2);
        expect(mocks.page.mock.calls.length).toBeLessThanOrEqual(2);
        log.mockRestore();
    });

    it('falls back to the nearest rank when the anchored photo was deleted', async () => {
        await renderGallery();
        host.dataset.anchorIndex = '1';
        generation = '2'; mocks.timeline.mockResolvedValue(timeline(1, '2'));
        mocks.locate.mockRejectedValue(new Error('not found'));
        await renderGallery({ background: true });
        expect(get(galleryView)).toMatchObject({ initialIndex: 1 });
    });

    it('uses ID selection for phone taps and supports deselection', async () => {
        mocks.mobile = true;
        activateGallery();
        await renderGallery();
        toggleGallerySelection(0);
        const button = document.createElement('button'); button.className = 'gallery-cell'; button.dataset.id = '2'; button.dataset.index = '1'; host.append(button);
        button.click();
        expect(state.selectedItems.has('file:2')).toBe(true);
        button.click();
        expect(state.selectedItems.has('file:2')).toBe(false);
        expect(mocks.preview).not.toHaveBeenCalled();
        toggleGallerySelection(-1);
        toggleGallerySelection(0);
        expect(state.selectedItems.size).toBe(0);
        button.dataset.id = '999'; button.click();
        host.click();
        expect(mocks.preview).not.toHaveBeenCalled();
    });

    it('keeps Photos navigation semantics and tolerates a missing host', async () => {
        const main = document.createElement('main'); main.className = 'main-content';
        const nav = document.createElement('button'); nav.id = 'nav-photos';
        const drive = document.createElement('button'); drive.className = 'drive-item'; drive.dataset.channelId = '1';
        document.body.append(main, nav, drive);
        setPhotosMode(true);
        expect(nav.getAttribute('aria-current')).toBe('page');
        expect(main.classList.contains('photos-mode')).toBe(true);
        setPhotosMode(false);
        expect(drive.getAttribute('aria-current')).toBe('page');
        expect(nav.hasAttribute('aria-current')).toBe(false);
        state.virtualView = null;
        enterPhotos(); enterPhotos();
        expect(state.virtualView).toBe('photos');
        exitPhotos(); exitPhotos();
        expect(state.virtualView).toBeNull();
        main.remove(); nav.remove(); drive.remove();
        teardownGallery(); host.remove();
        expect(activateGallery()()).toBeUndefined();
        await renderGallery();
    });
    it('opens with one metadata page and preserves selected IDs after eviction', async () => {
        await renderGallery();
        const view = get(galleryView);
        expect(view.status).toBe('ready');
        if (view.status !== 'ready') return;
        expect(view.source.retainedCount).toBe(128);
        expect(mocks.page).toHaveBeenCalledExactlyOnceWith('0', 128);
        toggleGallerySelection(0);
        expect(state.selectedItems.has('file:1')).toBe(true);
        for (let index = 128; index < 1000; index += 128) await view.source.get(index);
        expect(state.selectedItems.get('file:1')?.name).toBe('photo.jpg');
    });

    it('preserves a file anchor across background insertions', async () => {
        await renderGallery();
        const initial = get(galleryView);
        if (initial.status !== 'ready') throw new Error('missing gallery');
        await initial.source.get(130);
        host.dataset.anchorIndex = '130'; host.dataset.anchorOffset = '12';
        generation = '2';
        mocks.timeline.mockResolvedValue(timeline(1, '2'));
        mocks.locate.mockResolvedValue({ generation: '2', index: 131, cursor: '131' });
        await renderGallery({ background: true });
        expect(get(galleryView)).toMatchObject({ status: 'ready', initialIndex: 131, anchorOffset: 12 });
        expect(mocks.locate).toHaveBeenCalledWith(131, '2');
        expect(initial.source.retainedCount).toBe(0);
    });

    it('discards the old drive completion after switching drives', async () => {
        let resolve!: (value: MediaTimeline) => void;
        mocks.timeline.mockImplementationOnce(() => new Promise((done) => { resolve = done; }));
        const old = renderGallery();
        // Wait until the old drive's read is actually in flight: the drive has
        // to change under a request that has already been made, not one that
        // has yet to be.
        await vi.waitFor(() => expect(mocks.timeline).toHaveBeenCalled());
        state.activeChannel = { id: 2 } as typeof state.activeChannel;
        mocks.timeline.mockResolvedValue(timeline(2));
        await renderGallery();
        resolve(timeline(1));
        await old;
        const view = get(galleryView);
        expect(view.status === 'ready' ? view.source.timeline.channelId : 0).toBe(2);
    });

    it('passes a lazy source to the viewer instead of a metadata array', async () => {
        await renderGallery();
        const button = document.createElement('button'); button.className = 'gallery-cell'; button.dataset.id = '1'; button.dataset.index = '0'; host.append(button);
        button.click();
        await vi.waitFor(() => expect(mocks.preview).toHaveBeenCalledOnce());
        const [source, first] = mocks.preview.mock.calls[0];
        expect(Array.isArray(source)).toBe(false);
        expect(source.getPosition(first)).toEqual({ index: 0, total: 1000 });
        expect((await source.getNeighbor(first, 1)).id).toBe(2);
        expect(mocks.page).toHaveBeenCalledOnce();
        teardownGallery();
        expect(await source.getNeighbor(first, 1)).toBeNull();
        expect(source.getPosition(first)).toBeNull();
    });
});
