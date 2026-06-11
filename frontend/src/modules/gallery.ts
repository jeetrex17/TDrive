// Photos gallery: a flat, date-grouped grid of every image in the active
// drive. Thumbnails lazy-load as cells scroll into view (IntersectionObserver),
// and clicking a cell opens the shared lightbox with prev/next over the whole
// set. Entered via the Photos item in the sidebar; renders into #gallery-view,
// a sibling of #file-list that CSS shows only in photos mode.

import { state } from '../state';
import { getMedia, getThumbnail } from '../api';
import { openPreviewList } from './modals/preview';
import { clearSearch } from './search';
import type { FileItem } from '../types';

// Soft cap on the in-memory thumbnail-URL map (keyed channelId:msgId). The
// backend disk cache makes a re-fetch cheap, so this only bounds bookkeeping.
const THUMB_CACHE_MAX = 1500;

// Cap on cells holding a decoded image at once. Loaded <img>s retain their
// decoded bitmaps, so we unload the least-recently-loaded ones past this bound
// (they reload instantly from the thumb cache when scrolled back). This keeps
// memory flat on large libraries instead of growing with every scrolled cell.
const MAX_LOADED_CELLS = 240;

const lockSvg = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><rect x="5" y="11" width="14" height="10" rx="2"/><path stroke-linecap="round" stroke-linejoin="round" d="M8 11V7a4 4 0 018 0v4"/></svg>`;
const brokenSvg = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" aria-hidden="true"><rect x="3" y="5" width="18" height="14" rx="2"/><path stroke-linecap="round" stroke-linejoin="round" d="M3 16l5-5 4 4 3-3 6 6"/></svg>`;
const photosSvg = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true"><rect x="3" y="3" width="18" height="18" rx="2"/><circle cx="8.5" cy="8.5" r="1.6"/><path stroke-linecap="round" stroke-linejoin="round" d="M21 15l-5-5L5 21"/></svg>`;

let galleryEl: HTMLElement | null = null;
let observer: IntersectionObserver | null = null;
let renderToken = 0;
let currentItems: FileItem[] = [];
let currentChannelId = 0;

// "channelId:msgId" -> loaded thumbnail data URL. Keyed by drive because a
// Telegram msg id is only unique within its channel; reused across re-renders
// and handed to the lightbox as an instant placeholder.
const thumbCache = new Map<string, string>();

// Cells currently holding a decoded image, oldest first (FIFO eviction).
const loadedCells: HTMLElement[] = [];

export function setupGallery(): void {
    galleryEl = document.getElementById('gallery-view');
    setupGalleryClicks();
    // When the vault is unlocked (e.g. from the lightbox), let locked thumbnail
    // cells try again without waiting for a full gallery refresh.
    window.addEventListener('tdrive:unlocked', rearmLockedCells);
}

// setPhotosMode toggles the whole main view between the file list and the
// gallery (CSS keys off .photos-mode) and syncs the sidebar nav highlight:
// the Photos item is active in gallery view, the active drive in files view.
export function setPhotosMode(on: boolean): void {
    document.querySelector('.main-content')?.classList.toggle('photos-mode', on);
    document.getElementById('nav-photos')?.classList.toggle('active', on);
    const activeId = Number(state.activeChannel?.id || 0);
    document.querySelectorAll<HTMLElement>('.drive-item[data-channel-id]').forEach((el) => {
        const isActiveDrive = Number(el.dataset.channelId) === activeId;
        el.classList.toggle('active', isActiveDrive && !on);
    });
}

export async function renderGallery(): Promise<void> {
    if (!galleryEl) galleryEl = document.getElementById('gallery-view');
    if (!galleryEl) return;

    const token = ++renderToken;
    teardownObserver();
    loadedCells.length = 0;
    galleryEl.innerHTML = '<div class="gallery-status">Loading photos…</div>';

    const channelId = Number(state.activeChannel?.id || 0);

    let media: FileItem[];
    try {
        media = await getMedia();
    } catch (err) {
        console.error('ListMedia failed:', err);
        if (token === renderToken) {
            galleryEl.innerHTML = '<div class="gallery-status">Could not load photos.</div>';
        }
        return;
    }

    // A newer render started, or the user left photos mode, while we awaited.
    if (token !== renderToken || state.virtualView !== 'photos') return;

    currentItems = media;
    currentChannelId = channelId;

    if (media.length === 0) {
        galleryEl.innerHTML = `
            <div class="gallery-empty">
                <div class="gallery-empty-icon">${photosSvg}</div>
                <div class="gallery-empty-title">No photos yet</div>
                <div class="gallery-empty-sub">Images you upload to this drive show up here.</div>
            </div>`;
        return;
    }

    const frag = document.createDocumentFragment();
    let index = 0;
    for (const group of groupByMonth(media)) {
        const section = document.createElement('section');
        section.className = 'gallery-group';

        const header = document.createElement('div');
        header.className = 'gallery-group-header';
        header.textContent = group.label;
        section.appendChild(header);

        const grid = document.createElement('div');
        grid.className = 'gallery-grid';
        for (const item of group.items) {
            grid.appendChild(buildCell(item, index));
            index += 1;
        }
        section.appendChild(grid);
        frag.appendChild(section);
    }

    galleryEl.innerHTML = '';
    galleryEl.appendChild(frag);
    setupObserver();
    observeCells();
}

function buildCell(item: FileItem, index: number): HTMLElement {
    const cell = document.createElement('button');
    cell.type = 'button';
    cell.className = 'gallery-cell';
    cell.dataset.id = String(item.msgId);
    cell.dataset.index = String(index);
    cell.title = item.name;
    cell.setAttribute('aria-label', item.name);

    const img = document.createElement('img');
    img.className = 'gallery-thumb';
    img.alt = '';
    img.decoding = 'async';
    cell.appendChild(img);

    if (item.encrypted) {
        const lock = document.createElement('span');
        lock.className = 'gallery-lock';
        lock.innerHTML = lockSvg;
        cell.appendChild(lock);
    }
    // Thumbnails (even already-cached ones) load through the observer/loadCell
    // path so the loaded-cell budget stays accurate.
    return cell;
}

function setupObserver(): void {
    if (!galleryEl) return;
    observer = new IntersectionObserver(
        (entries) => {
            for (const entry of entries) {
                if (!entry.isIntersecting) continue;
                const cell = entry.target as HTMLElement;
                observer?.unobserve(cell);
                void loadCell(cell);
            }
        },
        { root: galleryEl, rootMargin: '320px 0px' },
    );
}

function observeCells(): void {
    if (!observer || !galleryEl) return;
    // Only arm genuinely-untouched cells; loaded/in-flight/errored cells are
    // either done or owned by an in-flight load.
    galleryEl
        .querySelectorAll('.gallery-cell:not(.is-loaded):not(.is-loading):not(.is-failed):not(.is-locked)')
        .forEach((cell) => observer!.observe(cell));
}

function teardownObserver(): void {
    if (observer) {
        observer.disconnect();
        observer = null;
    }
}

async function loadCell(cell: HTMLElement): Promise<void> {
    if (cell.classList.contains('is-loaded') || cell.classList.contains('is-loading')) return;
    const msgId = Number(cell.dataset.id || 0);
    const img = cell.querySelector('img.gallery-thumb') as HTMLImageElement | null;
    if (!msgId || !img) return;

    const channelId = currentChannelId;
    const key = `${channelId}:${msgId}`;

    const cached = thumbCache.get(key);
    if (cached) {
        img.src = cached;
        cell.classList.remove('is-failed', 'is-locked');
        cell.classList.add('is-loaded');
        registerLoaded(cell);
        return;
    }

    cell.classList.add('is-loading');
    const token = renderToken;
    try {
        const url = await getThumbnail(msgId);
        // Discard if a newer render started, the active drive changed mid-flight,
        // or this cell is gone — never paint or cache a stale/wrong-drive image.
        if (token !== renderToken || channelId !== currentChannelId || !cell.isConnected) return;
        cacheThumb(key, url);
        img.src = url;
        cell.classList.remove('is-loading');
        cell.classList.add('is-loaded');
        registerLoaded(cell);
    } catch (err) {
        if (token !== renderToken || channelId !== currentChannelId || !cell.isConnected) return;
        cell.classList.remove('is-loading');
        if (/password required/i.test(String(err))) {
            cell.classList.add('is-locked');
            cell.title = `${cell.dataset.name || ''} — locked, click to unlock`.trim();
        } else {
            cell.classList.add('is-failed');
            cell.title = `${cell.title} — couldn't load`;
            const badge = document.createElement('span');
            badge.className = 'gallery-broken';
            badge.innerHTML = brokenSvg;
            cell.appendChild(badge);
        }
    }
}

// registerLoaded tracks a cell holding a decoded image and unloads the oldest
// once we exceed the budget, keeping decoded-image memory bounded.
function registerLoaded(cell: HTMLElement): void {
    loadedCells.push(cell);
    while (loadedCells.length > MAX_LOADED_CELLS) {
        const old = loadedCells.shift();
        if (old && old !== cell) unloadCell(old);
    }
}

function unloadCell(cell: HTMLElement): void {
    if (!cell.isConnected) return;
    const img = cell.querySelector('img.gallery-thumb') as HTMLImageElement | null;
    if (img) img.removeAttribute('src');
    cell.classList.remove('is-loaded');
    // Re-arm so it reloads (instantly, from the thumb cache) when scrolled back.
    observer?.observe(cell);
}

function cacheThumb(key: string, url: string): void {
    thumbCache.set(key, url);
    if (thumbCache.size > THUMB_CACHE_MAX) {
        const oldest = thumbCache.keys().next().value;
        if (oldest !== undefined) thumbCache.delete(oldest);
    }
}

function setupGalleryClicks(): void {
    if (!galleryEl) return;
    galleryEl.addEventListener('click', (e) => {
        const cell = (e.target as HTMLElement).closest('.gallery-cell') as HTMLElement | null;
        if (!cell) return;
        const index = Number(cell.dataset.index ?? -1);
        if (index < 0 || index >= currentItems.length) return;
        openGalleryLightbox(index);
    });
}

function openGalleryLightbox(index: number): void {
    const channelId = currentChannelId;
    // Carry the fields the lightbox + info panel need: a download size (plaintext
    // for encrypted files), the loaded thumbnail as an instant placeholder, and
    // the metadata the info panel shows.
    const items = currentItems.map((it) => ({
        type: 'file',
        id: it.msgId,
        name: it.name,
        size: it.encrypted && it.plaintextSize > 0 ? it.plaintextSize : it.size,
        encrypted: it.encrypted,
        uploaderId: it.uploaderId,
        uploadTime: it.uploadTime,
        thumbUrl: thumbCache.get(`${channelId}:${it.msgId}`) || '',
    }));
    void openPreviewList(items, index);
}

// rearmLockedCells lets locked thumbnail cells retry after the vault unlocks.
function rearmLockedCells(): void {
    if (!observer || !galleryEl) return;
    galleryEl.querySelectorAll('.gallery-cell.is-locked').forEach((cell) => {
        cell.classList.remove('is-locked');
        observer!.observe(cell);
    });
}

// --- view switching (wired from the sidebar Photos item) ---

export function enterPhotos(): void {
    if (state.virtualView === 'photos') return;
    // The gallery is not a search surface: drop any active search so returning
    // to Files restores normal row interaction instead of search mode.
    clearSearch({ refresh: false });
    state.virtualView = 'photos';
    window.refreshFiles();
}

export function exitPhotos(): void {
    if (state.virtualView !== 'photos') return;
    state.virtualView = null;
    window.refreshFiles();
}

// --- date grouping ---

function groupByMonth(items: FileItem[]): { label: string; items: FileItem[] }[] {
    const groups: { label: string; items: FileItem[] }[] = [];
    let curKey = '';
    let cur: { label: string; items: FileItem[] } | null = null;
    for (const item of items) {
        const key = monthKey(item.uploadTime);
        if (!cur || key !== curKey) {
            cur = { label: monthLabel(item.uploadTime), items: [] };
            groups.push(cur);
            curKey = key;
        }
        cur.items.push(item);
    }
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
