// Photos coordinates a compact timeline and bounded pages. Rendering and
// image leases are shared across desktop and both mobile platforms; opening
// the viewer keeps this same source instead of copying the whole library.

import { state } from '../state';
import { isMobilePlatform } from '../api';
import { getMediaTimelineAnchors, getMediaTimelineSummary, locateMedia, type GalleryItem } from '../api/gallery';
import { GallerySource } from '../ui/gallery/gallery-source';
import { clearSearch } from './search';
import { appActions } from './app-actions';
import { canOwnerActOnFile } from './file-list';
import { updateSelectionBar } from './selection';
import { beginRender, cachedThumb, rearmLocked, setActive, setRoot, teardown as teardownGalleryController } from '../ui/gallery/gallery-controller';
import { galleryView } from '../ui/gallery/gallery-store';
import { bindLongPress, bindPullToRefresh } from '../ui/file-list/touch';
import { setSidebarPhotosActive } from '../ui/sidebar/sidebar-store';
import type { PreviewNavigationItem } from './modals/preview';
import { setFileThumbnailsActive } from '../ui/file-list/file-thumbnail-controller';

let galleryEl: HTMLElement | null = null;
let renderToken = 0;
let backgroundRenderToken = 0;
let currentSource: GallerySource | null = null;
let currentChannelId = 0;
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
    setSidebarPhotosActive(on);

    const activeId = Number(state.activeChannel?.id || 0);
    document.querySelectorAll<HTMLElement>('.drive-item[data-channel-id]').forEach((el) => {
        const isActiveDrive = Number(el.dataset.channelId) === activeId;
        el.classList.toggle('active', isActiveDrive && !on);
        if (isActiveDrive && !on) el.setAttribute('aria-current', 'page');
        else el.removeAttribute('aria-current');
    });
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
    const sameDrive = currentChannelId === channelId;
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
        const timeline = await getMediaTimelineSummary();
        if (timeline.channelId !== channelId) return;
        if (currentSource?.timeline.generation === timeline.generation && sameDrive) return;
        let restoredIndex = Math.min(anchorIndex, Math.max(0, timeline.totalCount - 1));
        if (anchor) {
            try { restoredIndex = (await locateMedia(anchor.msgId, timeline.generation)).index; }
            catch { /* A deleted anchor falls back to the closest surviving rank. */ }
        }
        next = new GallerySource(timeline, {
            maxPages: isMobilePlatform() ? 6 : 12,
            onStale: () => { void renderGallery({ background: true }); },
            loadAnchors: getMediaTimelineAnchors,
        });
        if (timeline.totalCount > 0) {
            if (restoredIndex > 0) {
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
    void openGalleryLightbox(item);
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
            const index = source.indexOf(Number(active.id))
                ?? (await locateMedia(Number(active.id), source.timeline.generation)).index;
            const neighbor = await source.get(index + direction);
            return neighbor ? previewItem(neighbor, channelId) : null;
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
