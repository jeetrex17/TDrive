// Photos has two views of one drive, and this is the seam between them: which
// one it opens on, what the switch offers, and that opening a tile scopes the
// existing grid to that folder rather than growing a second one.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import { get } from 'svelte/store';

const mocks = vi.hoisted(() => ({
    timeline: vi.fn(), folderTimeline: vi.fn(), page: vi.fn(), folderPage: vi.fn(), folders: vi.fn(),
}));
vi.mock('../../api', () => ({ isMobilePlatform: () => false, onRuntimeEvent: () => () => {} }));
vi.mock('../../api/gallery', () => ({
    getMediaTimelineSummary: mocks.timeline, getMediaTimelineAnchors: mocks.timeline,
    getMediaFolderTimeline: mocks.folderTimeline, listMediaPage: mocks.page,
    listMediaFolderPage: mocks.folderPage, listMediaFolders: mocks.folders, locateMedia: vi.fn(),
}));
vi.mock('../../modules/search', () => ({ clearSearch: vi.fn() }));
vi.mock('../../modules/app-actions', () => ({ appActions: () => ({ refreshFiles: vi.fn(), triggerRefresh: vi.fn() }) }));
vi.mock('../../modules/transfers', () => ({ chooseFilesForCurrentFolder: vi.fn() }));
vi.mock('../../modules/selection', () => ({ updateSelectionBar: vi.fn() }));
vi.mock('../file-list/touch', () => ({ bindLongPress: () => () => {}, bindPullToRefresh: () => () => {} }));
vi.mock('./gallery-controller', () => ({
    beginRender: vi.fn(), cachedThumb: () => '', rearmLocked: vi.fn(), setActive: vi.fn(),
    setRoot: vi.fn(), teardown: vi.fn(), registerCell: vi.fn(), unregisterCell: vi.fn(),
}));

import type { MediaFolder } from '../../api/gallery';
import { state } from '../../state';
import { activateGallery, renderGallery, teardownGallery } from '../../modules/gallery';
import { albumsView, galleryView, photosMode } from './gallery-store';
import PhotosSurface from './PhotosSurface.svelte';
import PhotosModeBar from './PhotosModeBar.svelte';

// The api boundary is mocked, so these are the normalized rows it hands over.
const CAMERA: MediaFolder = { folderId: 'd:camera', name: 'Camera', itemCount: 595, latestUploadTime: 9, coverMsgId: 5, coverRevision: 2, coverName: 'IMG_5.jpg' };
const SHOTS: MediaFolder = { folderId: 'd:shots', name: 'WhatsApp Animated Gifs', itemCount: 1053, latestUploadTime: 4, coverMsgId: 0, coverRevision: 0, coverName: '' };

function timeline(totalCount = 2) {
    return {
        channelId: 1, generation: 'g', totalCount, pageSize: 128,
        buckets: totalCount ? [{ key: '2026-09', startIndex: 0, count: totalCount, uploadTime: 1 }] : [],
        anchors: [],
    };
}
function page(count: number) {
    return {
        generation: 'g', startIndex: 0, nextCursor: '',
        items: Array.from({ length: count }, (_, index) => ({
            msgId: index + 1, name: `IMG_${index + 1}.jpg`, size: 10, parentId: '', uploadTime: 1,
            uploaderId: 1, encrypted: false, plaintextSize: 0, revision: 1, contentMsgId: index + 1, contentHash: '',
        })),
    };
}

let host: HTMLElement;
let surface: Record<string, unknown> | null = null;
let bar: Record<string, unknown> | null = null;

/** Mount what the shells mount: the switch above, the scroll container below. */
function show(): void {
    bar = mount(PhotosModeBar, { target: document.body });
    surface = mount(PhotosSurface, { target: host });
    flushSync();
}

beforeEach(() => {
    state.activeChannel = { id: 1 } as typeof state.activeChannel;
    state.virtualView = 'photos';
    state.selectedItems = new Map();
    mocks.folders.mockReset().mockResolvedValue([CAMERA, SHOTS]);
    mocks.timeline.mockReset().mockResolvedValue(timeline());
    mocks.folderTimeline.mockReset().mockResolvedValue(timeline());
    mocks.page.mockReset().mockResolvedValue(page(2));
    mocks.folderPage.mockReset().mockResolvedValue(page(2));
    host = document.createElement('div');
    host.id = 'gallery-view';
    document.body.append(host);
    activateGallery();
});

afterEach(async () => {
    teardownGallery();
    if (surface) await unmount(surface);
    if (bar) await unmount(bar);
    surface = null;
    bar = null;
    host.remove();
    galleryView.set({ status: 'loading' });
    albumsView.set({ status: 'loading' });
    photosMode.set({ kind: 'timeline' });
});

describe('Photos albums', () => {
    it('opens on the grid and labels every tile with its folder and its size', async () => {
        await renderGallery();
        show();
        expect(get(photosMode)).toEqual({ kind: 'albums' });
        const tiles = host.querySelectorAll<HTMLElement>('.album-tile');
        expect(tiles).toHaveLength(2);
        expect(tiles[0].getAttribute('aria-label')).toBe('Camera, 595 photos');
        expect(tiles[1].getAttribute('aria-label')).toBe(`WhatsApp Animated Gifs, ${(1053).toLocaleString()} photos`);
        // A folder with no renderable cover draws a glyph, never a broken image.
        expect(tiles[1].querySelector('.album-cover-glyph')).not.toBeNull();
        expect(tiles[0].querySelector('img')).not.toBeNull();
        // The grid is what is on screen, so no timeline page was ever read.
        expect(mocks.page).not.toHaveBeenCalled();
    });

    it('falls back to the timeline when there is no structure to show', async () => {
        mocks.folders.mockResolvedValue([CAMERA]);
        await renderGallery();
        show();
        expect(get(photosMode)).toEqual({ kind: 'timeline' });
        expect(host.querySelector('.album-tile')).toBeNull();
        expect(document.querySelector('.photos-modes')).toBeNull();

        mocks.folders.mockResolvedValue([]);
        teardownGallery();
        activateGallery();
        await renderGallery();
        expect(get(photosMode)).toEqual({ kind: 'timeline' });
    });

    it('keeps Photos usable when the folder list cannot be read', async () => {
        const log = vi.spyOn(console, 'warn').mockImplementation(() => {});
        mocks.folders.mockRejectedValue(new Error('offline'));
        await renderGallery();
        show();
        expect(get(photosMode)).toEqual({ kind: 'timeline' });
        expect(get(galleryView).status).toBe('ready');
        log.mockRestore();
    });

    it('switches between the grid and the whole timeline in place', async () => {
        await renderGallery();
        show();
        const buttons = document.querySelectorAll<HTMLButtonElement>('.photos-modes button');
        expect([...buttons].map((button) => button.getAttribute('aria-pressed'))).toEqual(['true', 'false']);
        buttons[1].click();
        await vi.waitFor(() => expect(get(photosMode)).toEqual({ kind: 'timeline' }));
        flushSync();
        expect(host.querySelector('.album-tile')).toBeNull();
        expect(mocks.page).toHaveBeenCalled();
        // The grid recomputes on the way back rather than resorting live.
        mocks.folders.mockClear();
        document.querySelector<HTMLButtonElement>('.photos-modes button')?.click();
        await vi.waitFor(() => expect(get(photosMode)).toEqual({ kind: 'albums' }));
        expect(mocks.folders).toHaveBeenCalledOnce();
    });

    it('opens a tile into the existing grid, scoped, and comes back out', async () => {
        await renderGallery();
        show();
        host.querySelector<HTMLButtonElement>('.album-tile')?.click();
        await vi.waitFor(() => expect(mocks.folderTimeline).toHaveBeenCalledWith('d:camera'));
        await vi.waitFor(() => { flushSync(); expect(host.querySelector('.gallery-cell')).not.toBeNull(); });
        expect(host.querySelector('.album-tile')).toBeNull();
        expect(mocks.folderPage).toHaveBeenCalledWith('d:camera', '', 128);
        expect(mocks.page).not.toHaveBeenCalled();

        const back = document.querySelector<HTMLButtonElement>('.album-back');
        expect(back?.textContent).toContain('Camera');
        back?.click();
        await vi.waitFor(() => expect(get(photosMode)).toEqual({ kind: 'albums' }));
        flushSync();
        expect(host.querySelectorAll('.album-tile')).toHaveLength(2);
    });

    it('moves the single tab stop with the arrow keys', async () => {
        await renderGallery();
        show();
        const tiles = () => host.querySelectorAll<HTMLElement>('.album-tile');
        expect([...tiles()].map((tile) => tile.getAttribute('tabindex'))).toEqual(['0', '-1']);
        tiles()[0].focus();
        tiles()[0].dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowRight', bubbles: true }));
        await vi.waitFor(() => { flushSync(); expect(document.activeElement).toBe(tiles()[1]); });
        expect([...tiles()].map((tile) => tile.getAttribute('tabindex'))).toEqual(['-1', '0']);
    });
});
