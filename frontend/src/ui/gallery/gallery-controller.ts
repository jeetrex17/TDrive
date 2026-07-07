// Imperative machinery behind the photos gallery: one IntersectionObserver
// for lazy thumbnail loading, an LRU thumbnail-URL cache, and FIFO eviction of
// decoded <img> bitmaps so memory stays flat on large libraries. The Svelte
// components render structure and register each cell here; this controller
// owns every load, cache, and eviction decision so that behavior is identical
// to the pre-Svelte gallery.
//
// It is a module singleton because the caches must outlive any single render:
// scrolling away and back, or switching drives and returning, reuses thumbs.

import { getThumbnail } from '../../api';

// Soft cap on the in-memory thumbnail-URL map (keyed channelId:msgId). The
// backend disk cache makes a re-fetch cheap, so this only bounds bookkeeping.
const THUMB_CACHE_MAX = 1500;

// Cap on cells holding a decoded image at once. Loaded <img>s retain their
// decoded bitmaps, so we unload the least-recently-loaded ones past this bound
// (they reload instantly from the thumb cache when scrolled back).
const MAX_LOADED_CELLS = 240;

export type CellStatus = 'idle' | 'loading' | 'loaded' | 'failed' | 'locked';

export interface CellPatch {
    status?: CellStatus;
    src?: string;
    title?: string;
}

export interface CellRegistration {
    msgId: number;
    apply: (patch: CellPatch) => void;
}

interface CellHandle extends CellRegistration {
    node: HTMLElement;
    status: CellStatus;
}

const handles = new Map<HTMLElement, CellHandle>();
// Cells currently holding a decoded image, oldest first (FIFO eviction). Kept
// consistent across renders by register/unregister alone — never bulk-cleared,
// because Svelte reuses keyed cells whose loaded state must keep being tracked.
const loadedHandles: CellHandle[] = [];
// "channelId:msgId" -> loaded thumbnail data URL.
const thumbCache = new Map<string, string>();

let observer: IntersectionObserver | null = null;
let rootEl: HTMLElement | null = null;
let currentChannelId = 0;

// setRoot installs the scroll container used as the observer root. Called once
// when the gallery component mounts.
export function setRoot(el: HTMLElement): void {
    if (rootEl === el && observer) return;
    if (observer) observer.disconnect();
    rootEl = el;
    observer = new IntersectionObserver(onIntersect, { root: el, rootMargin: '320px 0px' });
    // Re-observe any cells that registered before the root was ready.
    for (const handle of handles.values()) {
        if (handle.status === 'idle') observer.observe(handle.node);
    }
}

// beginRender records the drive the upcoming cells belong to, so an in-flight
// thumbnail load from a previous drive is discarded rather than painted onto a
// reused cell. Called before the orchestrator publishes new cells.
export function beginRender(channelId: number): void {
    currentChannelId = channelId;
}

export function registerCell(node: HTMLElement, reg: CellRegistration): void {
    const handle: CellHandle = { node, msgId: reg.msgId, apply: reg.apply, status: 'idle' };
    handles.set(node, handle);
    observer?.observe(node);
}

export function unregisterCell(node: HTMLElement): void {
    observer?.unobserve(node);
    const handle = handles.get(node);
    if (handle) {
        const idx = loadedHandles.indexOf(handle);
        if (idx >= 0) loadedHandles.splice(idx, 1);
    }
    handles.delete(node);
}

// rearmLocked lets locked cells retry after the vault unlocks, without a full
// gallery refresh.
export function rearmLocked(): void {
    for (const handle of handles.values()) {
        if (handle.status !== 'locked') continue;
        handle.status = 'idle';
        handle.apply({ status: 'idle' });
        observer?.observe(handle.node);
    }
}

// cachedThumb returns a loaded thumbnail URL for the lightbox placeholder.
export function cachedThumb(channelId: number, msgId: number): string {
    return thumbCache.get(`${channelId}:${msgId}`) || '';
}

function onIntersect(entries: IntersectionObserverEntry[]): void {
    for (const entry of entries) {
        if (!entry.isIntersecting) continue;
        const node = entry.target as HTMLElement;
        observer?.unobserve(node);
        const handle = handles.get(node);
        if (handle) void loadCell(handle);
    }
}

async function loadCell(handle: CellHandle): Promise<void> {
    if (handle.status === 'loaded' || handle.status === 'loading') return;

    const channelId = currentChannelId;
    const key = `${channelId}:${handle.msgId}`;

    const cached = thumbCache.get(key);
    if (cached) {
        handle.status = 'loaded';
        handle.apply({ status: 'loaded', src: cached });
        registerLoaded(handle);
        return;
    }

    handle.status = 'loading';
    handle.apply({ status: 'loading' });
    try {
        const url = await getThumbnail(handle.msgId);
        // Discard if the drive changed mid-flight or the cell was unregistered
        // (destroyed), so a stale or wrong-drive image is never painted/cached.
        // A reused same-drive cell still matches both guards and paints.
        if (channelId !== currentChannelId || !handles.has(handle.node)) return;
        cacheThumb(key, url);
        handle.status = 'loaded';
        handle.apply({ status: 'loaded', src: url });
        registerLoaded(handle);
    } catch (err) {
        if (channelId !== currentChannelId || !handles.has(handle.node)) return;
        if (/password required/i.test(String(err))) {
            handle.status = 'locked';
            handle.apply({ status: 'locked', title: 'locked, click to unlock' });
        } else {
            handle.status = 'failed';
            handle.apply({ status: 'failed', title: "couldn't load" });
        }
    }
}

// registerLoaded tracks a cell holding a decoded image and unloads the oldest
// once we exceed the budget, keeping decoded-image memory bounded.
function registerLoaded(handle: CellHandle): void {
    loadedHandles.push(handle);
    while (loadedHandles.length > MAX_LOADED_CELLS) {
        const old = loadedHandles.shift();
        if (old && old !== handle) unloadCell(old);
    }
}

function unloadCell(handle: CellHandle): void {
    if (!handles.has(handle.node)) return;
    handle.status = 'idle';
    handle.apply({ status: 'idle', src: '' });
    // Re-arm so it reloads (instantly, from the thumb cache) when scrolled back.
    observer?.observe(handle.node);
}

function cacheThumb(key: string, url: string): void {
    thumbCache.set(key, url);
    if (thumbCache.size > THUMB_CACHE_MAX) {
        const oldest = thumbCache.keys().next().value;
        if (oldest !== undefined) thumbCache.delete(oldest);
    }
}
