// The gallery owns DOM subscriptions; the shared rendition broker owns image
// bytes, URLs, deduplication and memory admission for both grid and viewer.
import { isMobilePlatform } from '../../api';
import { acquireRendition, subscribeRenditionReset } from '../../modules/renditions/runtime';
import type { RenditionLease } from '../../modules/renditions/broker';

export type CellStatus = 'idle' | 'loading' | 'loaded' | 'failed' | 'locked' | 'missing';
export interface CellPatch { status?: CellStatus; src?: string; title?: string }
export interface CellRegistration { msgId: number; revision?: number; apply: (patch: CellPatch) => void }
interface CellHandle extends CellRegistration {
    node: HTMLElement;
    channelId: number;
    status: CellStatus;
    src: string;
    attempt: number;
    retryTimer: number;
    lease: RenditionLease | null;
}

const handles = new Map<HTMLElement, CellHandle>();
let observer: IntersectionObserver | null = null;
let rootEl: HTMLElement | null = null;
let currentChannelId = 0;
let unsubscribeReset: (() => void) | null = null;
let active = true;

export function setRoot(el: HTMLElement): void {
    if (rootEl === el) return;
    observer?.disconnect();
    rootEl = el;
    observer = typeof IntersectionObserver === 'undefined' ? null : new IntersectionObserver(onIntersect, { root: el, rootMargin: '160px 0px' });
    for (const handle of handles.values()) observe(handle);
    document.addEventListener('visibilitychange', onVisibility);
    unsubscribeReset ??= subscribeRenditionReset(() => {
        for (const handle of handles.values()) {
            release(handle);
            handle.status = 'idle';
            handle.apply({ status: 'idle', src: '', title: '' });
            observer?.unobserve(handle.node);
            if (!document.hidden) observe(handle);
        }
    });
}

export function teardown(): void {
    observer?.disconnect();
    observer = null;
    rootEl = null;
    document.removeEventListener('visibilitychange', onVisibility);
    unsubscribeReset?.();
    unsubscribeReset = null;
    for (const handle of handles.values()) release(handle);
    handles.clear();
    active = true;
}

/** Photos and Files reuse one shell. Hidden Photos releases its image leases
 * immediately instead of retaining decoded pixels behind the file list. */
export function setActive(next: boolean): void {
    if (active === next) return;
    active = next;
    for (const handle of handles.values()) {
        observer?.unobserve(handle.node);
        if (active) observe(handle);
        else {
            release(handle);
            handle.status = 'idle';
            handle.apply({ status: 'idle', src: '', title: '' });
        }
    }
}

export function beginRender(channelId: number): void {
    if (channelId !== currentChannelId) {
        for (const handle of handles.values()) {
            release(handle);
            handle.apply({ status: 'idle', src: '', title: '' });
        }
    }
    currentChannelId = channelId;
}

export function registerCell(node: HTMLElement, reg: CellRegistration): void {
    unregisterCell(node);
    const handle: CellHandle = { ...reg, node, channelId: currentChannelId, status: 'idle', src: '', attempt: 0, retryTimer: 0, lease: null };
    handles.set(node, handle);
    observe(handle);
}

export function unregisterCell(node: HTMLElement): void {
    observer?.unobserve(node);
    const handle = handles.get(node);
    if (handle) release(handle);
    handles.delete(node);
}

export function rearmLocked(): void {
    for (const handle of handles.values()) {
        if (handle.status !== 'locked') continue;
        handle.status = 'idle';
        handle.attempt = 0;
        handle.apply({ status: 'idle', title: '' });
        observer?.unobserve(handle.node);
        observe(handle);
    }
}

export function rearmMissing(msgId?: number): void {
    for (const handle of handles.values()) {
        if (msgId !== undefined && handle.msgId !== msgId) continue;
        // A targeted ready event can race a pending 404. Releasing that lease
        // fences the late response before asking for the now-available bytes.
        if (handle.status !== 'missing' && !(msgId !== undefined && handle.status === 'loading')) continue;
        release(handle);
        handle.status = 'idle';
        handle.apply({ status: 'idle', title: '' });
        observer?.unobserve(handle.node);
        observe(handle);
    }
}

/** Borrow only a currently leased URL for the opening transition. The viewer
 * acquires its own lease before retaining the image. */
export function cachedThumb(channelId: number, msgId: number): string {
    for (const handle of handles.values()) {
        if (handle.channelId === channelId && handle.msgId === msgId && handle.status === 'loaded') return handle.src;
    }
    return '';
}

function observe(handle: CellHandle): void {
    if (!active) return;
    if (observer) observer.observe(handle.node);
    else if (rootEl) void load(handle);
}

function onIntersect(entries: IntersectionObserverEntry[]): void {
    for (const entry of entries) {
        const handle = handles.get(entry.target as HTMLElement);
        if (!handle) continue;
        if (entry.isIntersecting) {
            if (handle.status === 'idle') void load(handle);
        } else {
            release(handle);
            handle.status = 'idle';
            handle.apply({ status: 'idle', src: '', title: '' });
        }
    }
}

function onVisibility(): void {
    for (const handle of handles.values()) {
        if (document.hidden) {
            release(handle);
            handle.status = 'idle';
            handle.apply({ status: 'idle', src: '', title: '' });
        } else {
            observer?.unobserve(handle.node);
            observe(handle);
        }
    }
}

function release(handle: CellHandle): void {
    window.clearTimeout(handle.retryTimer);
    handle.retryTimer = 0;
    const lease = handle.lease;
    handle.lease = null;
    handle.src = '';
    lease?.release();
}

async function load(handle: CellHandle): Promise<void> {
    if (!active || document.hidden || handle.lease || handle.channelId !== currentChannelId) return;
    handle.status = 'loading';
    handle.apply({ status: 'loading' });
    const lease = acquireRendition({ channelId: handle.channelId, fileId: handle.msgId, revision: handle.revision ?? 0, kind: 'thumbnail' }, 'visible');
    handle.lease = lease;
    try {
        const asset = await lease.promise;
        if (!isCurrent(handle, lease)) return;
        handle.status = 'loaded';
        handle.src = asset.url;
        handle.apply({ status: 'loaded', src: asset.url, title: '' });
    } catch (error) {
        if (!isCurrent(handle, lease)) return;
        release(handle);
        const detail = error as { code?: string; retryAfterMs?: number };
        if (detail.code === 'encryption_password_required' || /password required/i.test(String(error))) {
            handle.status = 'locked';
            handle.apply({ status: 'locked', title: isMobilePlatform() ? 'locked, tap to unlock' : 'locked, click to unlock' });
            return;
        }
        if (detail.code === 'missing_rendition') {
            handle.status = 'missing';
            handle.apply({ status: 'missing', title: 'preview not available yet' });
            return;
        }
        const delay = retryDelay(error, handle.attempt);
        if (delay === null) {
            handle.status = 'failed';
            handle.apply({ status: 'failed', title: "couldn't load" });
            return;
        }
        handle.attempt += 1;
        handle.retryTimer = window.setTimeout(() => {
            handle.retryTimer = 0;
            if (handles.get(handle.node) === handle) void load(handle);
        }, delay);
    }
}

function isCurrent(handle: CellHandle, lease: RenditionLease): boolean {
    return handle.channelId === currentChannelId && handles.get(handle.node) === handle && handle.lease === lease;
}

export function retryDelay(error: unknown, attempt: number): number | null {
    if (attempt >= 3) return null;
    const advised = Number((error as { retryAfterMs?: number } | null)?.retryAfterMs ?? 0);
    if (Number.isFinite(advised) && advised > 0) return advised + 1000;
    const message = String(error);
    if (!/flood|rate.?limit|timeout/i.test(message)) return null;
    const wait = /wait[:_\s]*(\d+)/i.exec(message);
    // A server deadline is a lower bound, never clamped to our backoff cap.
    return wait ? Number(wait[1]) * 1000 + 1000 : Math.min(90_000, 8_000 * 2 ** attempt);
}
