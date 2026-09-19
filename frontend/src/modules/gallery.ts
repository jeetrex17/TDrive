// Photos coordinates a compact timeline and bounded pages. Rendering and
// image leases are shared across desktop and both mobile platforms; opening
// the viewer keeps this same source instead of copying the whole library.

import { tick } from 'svelte';
import { get } from 'svelte/store';
import { state } from '../state';
import { isMobilePlatform } from '../api';
import {
    getMediaFolderTimeline, getMediaTimelineAnchors, getMediaTimelineSummary,
    listMediaFolderPage, listMediaFolders, locateMedia, type GalleryItem,
} from '../api/gallery';
import { GallerySource } from '../ui/gallery/gallery-source';
import { clearSearch } from './search';
import { appActions } from './app-actions';
import { canOwnerActOnFile } from './file-list';
import { updateSelectionBar } from './selection';
import { beginRender, cachedThumb, rearmLocked, setActive, setRoot, teardown as teardownGalleryController } from '../ui/gallery/gallery-controller';
import { albumsView, galleryView, photosMode, type PhotosMode } from '../ui/gallery/gallery-store';
import { albumsWorthShowing, buildAlbumTiles, type AlbumTile } from '../ui/gallery/album-view';
import { bindLongPress, bindPullToRefresh } from '../ui/file-list/touch';
import { clearSidebarVirtualView, setSidebarVirtualView } from '../ui/sidebar/sidebar-store';
import type { PreviewNavigationItem } from './modals/preview';
import { setFileThumbnailsActive } from '../ui/file-list/file-thumbnail-controller';
import { isVideoFile } from './media-types';

let galleryEl: HTMLElement | null = null;
let renderToken = 0;
let backgroundRenderToken = 0;
let currentSource: GallerySource | null = null;
let currentChannelId = 0;
/** Which photos `currentSource` holds: null for the drive, else a folder id. */
let currentScope: string | null = null;
let albumsChannelId = 0;
let albumsToken = 0;
let touchCleanups: Array<() => void> = [];

export function activateGallery(): () => void {
    const host = document.getElementById('gallery-view');
    if (!host) return () => {};

    if (galleryEl) teardownGallery();
    galleryEl = host;
    setRoot(host);

    // Click delegation stays on the stable host, matching the pre-Svelte path.
    host.addEventListener('click', onGalleryClick);
    // When the vault unlocks (e.g. from the lightbox), let locked cells retry
    // without waiting for a full gallery refresh.
    window.addEventListener('tdrive:unlocked', rearmLocked);
    // Phone gestures: a long press starts a selection, a pull from the top
    // refreshes. Both share the file list's helpers so they feel the same.
    if (isMobilePlatform()) {
        touchCleanups = [
            bindLongPress(host, '.gallery-cell', (cell) => toggleGallerySelection(Number(cell.dataset.index ?? -1))),
            bindPullToRefresh(host, () => appActions().triggerRefresh()),
        ];
    }

    return teardownGallery;
}

export function teardownGallery(): void {
    renderToken += 1;
    backgroundRenderToken += 1;
    galleryEl?.removeEventListener('click', onGalleryClick);
    window.removeEventListener('tdrive:unlocked', rearmLocked);
    for (const cleanup of touchCleanups.splice(0)) cleanup();
    teardownGalleryController();
    galleryEl = null;
    currentSource?.dispose();
    currentSource = null;
    currentChannelId = 0;
    currentScope = null;
    // The next activation is an entry, and an entry recomputes the grid.
    albumsChannelId = 0;
}

// The gallery reuses the file list's selection, keyed the way its rows are,
// so the selection bar's Move and Delete work on photos unchanged.
export function toggleGallerySelection(index: number): void {
    const item = currentSource?.peek(index);
    if (!item) return;
    const key = `file:${item.msgId}`;
    const selected = new Map(state.selectedItems);
    if (selected.has(key)) {
        selected.delete(key);
    } else {
        const mine = canOwnerActOnFile(item);
        selected.set(key, {
            type: 'file',
            id: item.msgId,
            name: item.name,
            size: item.encrypted && item.plaintextSize > 0 ? item.plaintextSize : item.size,
            source: 'fs',
            parentId: item.parentId,
            uploaderID: item.uploaderId,
            canDelete: mine,
            canRename: mine,
        });
    }
    state.selectedItems = selected;
    updateSelectionBar();
}

// setPhotosMode toggles the whole main view between the file list and the
// gallery (CSS keys off .photos-mode) and syncs the sidebar nav highlight:
// the Photos item is active in gallery view, the active drive in files view.
export function setPhotosMode(on: boolean): void {
    setActive(on);
    setFileThumbnailsActive(!on);
    document.querySelector('.main-content')?.classList.toggle('photos-mode', on);
    const photosNav = document.getElementById('nav-photos');
    photosNav?.classList.toggle('active', on);
    if (on) photosNav?.setAttribute('aria-current', 'page');
    else photosNav?.removeAttribute('aria-current');
    if (on) setSidebarVirtualView('photos'); else clearSidebarVirtualView('photos');

    const activeId = Number(state.activeChannel?.id || 0);
    document.querySelectorAll<HTMLElement>('.drive-item[data-channel-id]').forEach((el) => {
        const isActiveDrive = Number(el.dataset.channelId) === activeId;
        el.classList.toggle('active', isActiveDrive && !on);
        if (isActiveDrive && !on) el.setAttribute('aria-current', 'page');
        else el.removeAttribute('aria-current');
    });
}

// --- albums: the folder grid, and what a tile opens ---

/**
 * Recompute the album grid for `channelId`. Names and counts are local, so a
 * failure here is never a reason to refuse Photos: the drive still has one
 * timeline, and falling back to it is better than an error where photos go.
 */
async function loadAlbums(channelId: number): Promise<AlbumTile[]> {
    const token = ++albumsToken;
    albumsChannelId = channelId;
    albumsView.set({ status: 'loading' });
    let tiles: AlbumTile[] = [];
    try {
        tiles = buildAlbumTiles(await listMediaFolders(), channelId);
    } catch (error) {
        console.warn('Album folders failed:', error);
    }
    if (token !== albumsToken) return [];
    albumsView.set({ status: 'ready', tiles });
    return tiles;
}

/** What Photos opens on: the grid when there is structure, else the timeline. */
function defaultPhotosMode(tiles: readonly AlbumTile[]): PhotosMode {
    return albumsWorthShowing(tiles) ? { kind: 'albums' } : { kind: 'timeline' };
}

/** Release the timeline's pages and leases; the grid is what is on screen. */
function dropGallerySource(): void {
    currentSource?.dispose();
    currentSource = null;
    currentChannelId = 0;
    currentScope = null;
    galleryView.set({ status: 'loading' });
}

function sameMode(left: PhotosMode, right: PhotosMode): boolean {
    if (left.kind !== right.kind) return false;
    return left.kind !== 'album' || left.tile.folderId === (right as { tile: AlbumTile }).tile.folderId;
}

/**
 * Switch what Photos shows. The grid, the timeline and one album are three
 * views of the same drive, so this re-renders in place rather than navigating
 * -- Photos stays the single nav destination it already is.
 */
/** Where the album grid was scrolled to when an album was opened from it. */
let albumsScrollTop = 0;

export async function showPhotos(mode: PhotosMode): Promise<void> {
    const previous = get(photosMode);
    if (sameMode(previous, mode)) return;
    if (previous.kind === 'albums' && galleryEl) albumsScrollTop = galleryEl.scrollTop;
    photosMode.set(mode);
    if (galleryEl) galleryEl.scrollTop = 0;
    // Asking for the grid is an entry: its counts are recomputed, and the
    // explicit choice then survives the render below.
    if (mode.kind === 'albums') await loadAlbums(Number(state.activeChannel?.id || 0));
    await renderGallery();
    if (!galleryEl || get(photosMode) !== mode) return;
    await tick();
    // Back lands where the grid was left, not at the top of it.
    if (mode.kind === 'albums' && previous.kind === 'album') galleryEl.scrollTop = albumsScrollTop;
    // The tile or cell that had the keyboard is gone with the old view; the
    // first of the new one takes it, unless something else already has focus.
    if (document.activeElement === document.body || galleryEl.contains(document.activeElement)) {
        galleryEl.querySelector<HTMLElement>(mode.kind === 'albums' ? '.album-tile' : '.gallery-cell')?.focus({ preventScroll: true });
    }
}

interface GalleryRefreshOptions {
    background?: boolean;
    staleRetry?: boolean;
}

export async function renderGallery({ background = false, staleRetry = false }: GalleryRefreshOptions = {}): Promise<void> {
    if (!galleryEl || galleryEl !== document.getElementById('gallery-view')) return;
    const token = background ? renderToken : ++renderToken;
    const backgroundToken = background ? ++backgroundRenderToken : 0;
    const channelId = Number(state.activeChannel?.id || 0);
    // Arriving in Photos, or in another drive, is what recomputes the grid and
    // picks the view. A refresh while the grid is already open deliberately
    // does not: photos landing mid-scroll must not resort tiles under a thumb.
    if (albumsChannelId !== channelId) {
        // Another drive: its grid is dropped before this one's folders are
        // asked for, or the old timeline sits on screen until they arrive.
        if (currentChannelId !== channelId) dropGallerySource();
        photosMode.set(defaultPhotosMode(await loadAlbums(channelId)));
        if (token !== renderToken || !galleryEl) return;
    }
    const mode = get(photosMode);
    if (mode.kind === 'albums') {
        // The grid renders from albumsView; the timeline's pages would only sit
        // in memory behind it, and its channel is what arms the cover loads.
        dropGallerySource();
        beginRender(channelId);
        return;
    }
    const scope = mode.kind === 'album' ? mode.tile.folderId : null;
    const sameDrive = currentChannelId === channelId && currentScope === scope;
    const anchorIndex = sameDrive ? Number(galleryEl.dataset.anchorIndex ?? 0) : 0;
    const anchor = sameDrive ? currentSource?.peek(anchorIndex) : undefined;
    const anchorOffset = sameDrive ? Number(galleryEl.dataset.anchorOffset ?? 0) : 0;
    if (!sameDrive) {
        currentSource?.dispose();
        currentSource = null;
        galleryEl.scrollTop = 0;
    }
    if (!currentSource) galleryView.set({ status: 'loading' });
    let next: GallerySource | null = null;
    try {
        const timeline = scope === null ? await getMediaTimelineSummary() : await getMediaFolderTimeline(scope);
        if (timeline.channelId !== channelId) return;
        if (currentSource?.timeline.generation === timeline.generation && sameDrive) return;
        let restoredIndex = Math.min(anchorIndex, Math.max(0, timeline.totalCount - 1));
        // LocateMedia ranks the whole drive, which is not the rank inside a
        // folder. A scoped view keeps the positional anchor instead.
        if (anchor && scope === null) {
            try { restoredIndex = (await locateMedia(anchor.msgId, timeline.generation)).index; }
            catch { /* A deleted anchor falls back to the closest surviving rank. */ }
        }
        next = new GallerySource(timeline, {
            maxPages: isMobilePlatform() ? 6 : 12,
            // Only a source that is live re-renders itself, and once. While it
            // is still being built the same stale page rejects the get() below
            // and the catch owns the retry; letting both run made every failed
            // render spawn two more, each with a fresh source, until a drive
            // whose two calls disagreed was asking for its timeline tens of
            // thousands of times a second.
            onStale: () => {
                if (currentSource !== next) return;
                void renderGallery({ background: true, staleRetry: true });
            },
            // A folder carries no anchor index: its pages are reached by
            // following each page's next cursor, which the source learns.
            ...(scope === null
                ? { loadAnchors: getMediaTimelineAnchors }
                : { load: (cursor: string, limit: number) => listMediaFolderPage(scope, cursor, limit) }),
        });
        if (timeline.totalCount > 0) {
            if (restoredIndex > 0 && scope === null) {
                const anchors = await getMediaTimelineAnchors(timeline.generation);
                next.installAnchors(anchors);
            }
            await next.get(restoredIndex);
        }
        if (token !== renderToken || (background && backgroundToken !== backgroundRenderToken)
            || state.virtualView !== 'photos' || Number(state.activeChannel?.id ?? 0) !== channelId) {
            next.dispose();
            return;
        }
        currentSource?.dispose();
        currentSource = next;
        currentChannelId = channelId;
        currentScope = scope;
        beginRender(channelId);
        if (timeline.totalCount === 0) galleryView.set({ status: 'empty' });
        else {
            galleryView.set({ status: 'ready', source: next, ...(sameDrive && anchorIndex > 0 ? { initialIndex: restoredIndex, anchorOffset } : {}) });
            const source = next;
            requestAnimationFrame(() => {
                if (currentSource !== source || source.timeline.anchors.length > 0) return;
                void source.requestAnchors()
                    .catch((error: unknown) => {
                        if (!/stale/i.test(String(error))) console.error('Gallery anchors failed:', error);
                    });
            });
        }
    } catch (error) {
        next?.dispose();
        if (/gallery snapshot is stale/i.test(String(error)) && !staleRetry) {
            await renderGallery({ background: true, staleRetry: true });
            return;
        }
        console.error('Gallery metadata failed:', error);
        if (token === renderToken && !currentSource) galleryView.set({ status: 'error' });
        else if (token === renderToken) currentSource?.reportRefreshError();
    }
}

function onGalleryClick(event: MouseEvent): void {
    const cell = (event.target as HTMLElement).closest<HTMLElement>('button.gallery-cell');
    if (!cell) return;
    const index = Number(cell.dataset.index ?? -1);
    const item = currentSource?.peek(index);
    if (!item || item.msgId !== Number(cell.dataset.id)) return;
    if (isMobilePlatform() && state.selectedItems.size > 0) {
        toggleGallerySelection(index);
        return;
    }
    if (isVideoFile(item.name)) {
        void openGalleryVideo(item);
        return;
    }
    void openGalleryLightbox(item);
}

async function openGalleryVideo(item: GalleryItem): Promise<void> {
    const source = currentSource;
    const channelId = currentChannelId;
    const video = await import('./modals/video');
    // Dynamic module loading can finish after a drive switch. Identity rather
    // than a message ID prevents opening another drive's same-numbered file.
    if (currentSource !== source || currentChannelId !== channelId || source?.indexOf(item.msgId) === undefined) return;
    await video.openVideoModal({
        id: item.msgId,
        name: item.name,
        size: item.encrypted && item.plaintextSize > 0 ? item.plaintextSize : item.size,
        encrypted: item.encrypted,
    });
}

function previewItem(item: GalleryItem, channelId: number): PreviewNavigationItem {
    return {
        type: 'file', id: item.msgId, name: item.name,
        size: item.encrypted && item.plaintextSize > 0 ? item.plaintextSize : item.size,
        encrypted: item.encrypted, uploaderId: item.uploaderId, uploadTime: item.uploadTime,
        channel_id: channelId, content_revision: item.revision,
        thumbUrl: cachedThumb(channelId, item.msgId),
    };
}

async function openGalleryLightbox(item: GalleryItem): Promise<void> {
    const channelId = currentChannelId;
    const preview = await import('./modals/preview');
    if (channelId !== currentChannelId) return;
    preview.activatePreviewModal();
    await preview.openPreviewSource({
        async getNeighbor(active, direction) {
            const source = currentSource;
            if (!source || currentChannelId !== channelId) return null;
            // LocateMedia ranks the whole drive. Inside an album that rank
            // addresses a different photo, so an evicted page ends navigation
            // there instead of jumping somewhere the viewer never was.
            const index = source.indexOf(Number(active.id))
                ?? (currentScope === null ? (await locateMedia(Number(active.id), source.timeline.generation)).index : undefined);
            if (index === undefined) return null;
            // The image viewer must never attempt an original-image request for
            // a video. Scan only one bounded neighbor window; this keeps a run
            // of videos from turning a next/previous tap into an unbounded
            // metadata walk through a large gallery.
            for (let offset = 1; offset <= 128; offset += 1) {
                const neighbor = await source.get(index + direction * offset);
                if (!neighbor) return null;
                if (!isVideoFile(neighbor.name)) return previewItem(neighbor, channelId);
            }
            return null;
        },
        getPosition(active) {
            if (!currentSource || currentChannelId !== channelId) return null;
            const index = currentSource.indexOf(Number(active.id));
            return index === undefined ? null : { index, total: currentSource.timeline.totalCount };
        },
    }, previewItem(item, channelId));
}

// --- view switching (wired from the sidebar Photos item) ---

export function enterPhotos(): void {
    if (state.virtualView === 'photos') return;
    // Entering recomputes the album grid and picks the view again, so a folder
    // that filled up or emptied while away is reflected on arrival.
    albumsChannelId = 0;
    // The gallery is not a search surface: drop any active search so returning
    // to Files restores normal row interaction instead of search mode.
    clearSearch({ refresh: false });
    state.virtualView = 'photos';
    appActions().refreshFiles();
}

export function exitPhotos(): void {
    if (state.virtualView !== 'photos') return;
    state.virtualView = null;
    appActions().refreshFiles({ background: true });
}
