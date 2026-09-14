// Photos gallery: a flat, date-grouped grid of every image in the active
// drive. Thumbnails lazy-load as cells scroll into view, and clicking a cell
// opens the shared lightbox with prev/next over the whole set. Entered via the
// Photos item in the sidebar; renders into #gallery-view, a sibling of
// #file-list that CSS shows only in photos mode.
//
// This module orchestrates: it fetches media, groups it, and drives view
// switching. The grid rendering is Gallery.svelte; the lazy-load / eviction /
// thumbnail-cache machinery lives in ui/gallery/gallery-controller.ts, which
// owns the IntersectionObserver rooted on the stable #gallery-view host.

import { state } from '../state';
import { getMedia } from '../api';
import { clearSearch } from './search';
import { appActions } from './app-actions';
import { beginRender, cachedThumb, rearmLocked, setRoot, teardown as teardownGalleryController } from '../ui/gallery/gallery-controller';
import { galleryView, type GalleryGroup } from '../ui/gallery/gallery-store';
import { setSidebarPhotosActive } from '../ui/sidebar/sidebar-store';
import type { FileItem } from '../types';

let galleryEl: HTMLElement | null = null;
let renderToken = 0;
let backgroundRenderToken = 0;
let currentItems: FileItem[] = [];
let currentChannelId = 0;

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

    return teardownGallery;
}

export function teardownGallery(): void {
    renderToken += 1;
    backgroundRenderToken += 1;
    galleryEl?.removeEventListener('click', onGalleryClick);
    window.removeEventListener('tdrive:unlocked', rearmLocked);
    teardownGalleryController();
    galleryEl = null;
    currentItems = [];
    currentChannelId = 0;
}

// setPhotosMode toggles the whole main view between the file list and the
// gallery (CSS keys off .photos-mode) and syncs the sidebar nav highlight:
// the Photos item is active in gallery view, the active drive in files view.
export function setPhotosMode(on: boolean): void {
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
}

export async function renderGallery({ background = false }: GalleryRefreshOptions = {}): Promise<void> {
    if (!galleryEl || galleryEl !== document.getElementById('gallery-view')) return;

    const token = background ? renderToken : ++renderToken;
    const backgroundToken = background ? ++backgroundRenderToken : 0;
    const channelId = Number(state.activeChannel?.id || 0);
    if (!background) galleryView.set({ status: 'loading' });

    let media: FileItem[];
    try {
        media = await getMedia();
    } catch (err) {
        console.error('ListMedia failed:', err);
        if (token === renderToken && !background) galleryView.set({ status: 'error' });
        return;
    }

    // Foreground navigation invalidates background work; background refreshes
    // only compete with other background refreshes after they have data to commit.
    if (
        token !== renderToken
        || (background && backgroundToken !== backgroundRenderToken)
        || state.virtualView !== 'photos'
        || Number(state.activeChannel?.id ?? 0) !== channelId
    ) return;

    currentItems = media;
    currentChannelId = channelId;
    beginRender(channelId);

    if (media.length === 0) {
        galleryView.set({ status: 'empty' });
        return;
    }
    galleryView.set({ status: 'ready', groups: groupByMonth(media) });
}

function onGalleryClick(event: MouseEvent): void {
    const cell = (event.target as HTMLElement).closest('.gallery-cell') as HTMLElement | null;
    if (!cell) return;
    const index = Number(cell.dataset.index ?? -1);
    if (index < 0 || index >= currentItems.length) return;
    void openGalleryLightbox(index);
}

async function openGalleryLightbox(index: number): Promise<void> {
    const channelId = currentChannelId;
    // Carry the fields the lightbox + info panel need: a download size
    // (plaintext for encrypted files), the loaded thumbnail as an instant
    // placeholder, and the metadata the info panel shows.
    const items = currentItems.map((it) => ({
        type: 'file',
        id: it.msgId,
        name: it.name,
        size: it.encrypted && it.plaintextSize > 0 ? it.plaintextSize : it.size,
        encrypted: it.encrypted,
        uploaderId: it.uploaderId,
        uploadTime: it.uploadTime,
        thumbUrl: cachedThumb(channelId, it.msgId),
    }));
    const preview = await import('./modals/preview');
    preview.activatePreviewModal();
    await preview.openPreviewList(items, index);
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

// --- date grouping ---

function groupByMonth(items: FileItem[]): GalleryGroup[] {
    const groups: GalleryGroup[] = [];
    let curKey = '';
    let cur: GalleryGroup | null = null;
    items.forEach((item, index) => {
        const key = monthKey(item.uploadTime);
        if (!cur || key !== curKey) {
            cur = { label: monthLabel(item.uploadTime), cells: [] };
            groups.push(cur);
            curKey = key;
        }
        cur.cells.push({ item, index });
    });
    return groups;
}

function monthKey(unixSec: number): string {
    if (!unixSec) return 'unknown';
    const d = new Date(unixSec * 1000);
    return `${d.getFullYear()}-${d.getMonth()}`;
}

function monthLabel(unixSec: number): string {
    if (!unixSec) return 'Unknown date';
    const d = new Date(unixSec * 1000);
    return d.toLocaleDateString(undefined, { month: 'long', year: 'numeric' });
}
