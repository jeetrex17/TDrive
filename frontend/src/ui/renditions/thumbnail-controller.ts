// Viewport ownership for small image renditions. A controller owns one scroll
// surface; the shared rendition broker still owns bytes, request deduplication,
// memory limits and object URL lifetime across every surface.
import { isMobilePlatform } from '../../api';
import { acquireRendition, subscribeRenditionReset } from '../../modules/renditions/runtime';
import type { RenditionLease } from '../../modules/renditions/broker';

export type ThumbnailStatus = 'idle' | 'loading' | 'loaded' | 'failed' | 'locked' | 'missing';

export interface ThumbnailPatch {
    status?: ThumbnailStatus;
    src?: string;
    title?: string;
}

export interface ThumbnailRegistration {
    channelId?: number;
    fileId: number;
    revision?: number;
    apply: (patch: ThumbnailPatch) => void;
}

interface ThumbnailHandle extends ThumbnailRegistration {
    node: HTMLElement;
    channelId: number;
    status: ThumbnailStatus;
    src: string;
    attempt: number;
    retryTimer: number;
    lease: RenditionLease | null;
}

export interface ThumbnailController {
    setRoot: (root: HTMLElement) => void;
    teardown: () => void;
    setActive: (active: boolean) => void;
    beginRender: (channelId: number) => void;
    register: (node: HTMLElement, registration: ThumbnailRegistration) => void;
    unregister: (node: HTMLElement) => void;
    rearmLocked: () => void;
    rearmMissing: (fileId?: number) => void;
    cached: (channelId: number, fileId: number) => string;
}

interface ThumbnailControllerOptions {
    rootMargin?: string;
}

/**
 * Creates a lightweight viewport controller. Each mounted cell costs one map
 * entry, while only intersecting cells hold rendition leases. This makes work
 * proportional to the visible window rather than the folder or library size.
 */
export function createThumbnailController(options: ThumbnailControllerOptions = {}): ThumbnailController {
    const handles = new Map<HTMLElement, ThumbnailHandle>();
    let observer: IntersectionObserver | null = null;
    let root: HTMLElement | null = null;
    let currentChannelId = 0;
    let unsubscribeReset: (() => void) | null = null;
    let watchingVisibility = false;
    let active = true;

    function observe(handle: ThumbnailHandle): void {
        if (!active || handle.channelId !== currentChannelId) return;
        if (observer) observer.observe(handle.node);
        else if (root) void load(handle);
    }

    function release(handle: ThumbnailHandle): void {
        window.clearTimeout(handle.retryTimer);
        handle.retryTimer = 0;
        const lease = handle.lease;
        handle.lease = null;
        handle.src = '';
        lease?.release();
    }

    function reset(handle: ThumbnailHandle): void {
        release(handle);
        handle.status = 'idle';
        handle.apply({ status: 'idle', src: '', title: '' });
    }

    function isCurrent(handle: ThumbnailHandle, lease: RenditionLease): boolean {
        return active
            && handle.channelId === currentChannelId
            && handles.get(handle.node) === handle
            && handle.lease === lease;
    }

    async function load(handle: ThumbnailHandle): Promise<void> {
        if (!active || document.hidden || handle.lease || handle.channelId !== currentChannelId) return;
        handle.status = 'loading';
        handle.apply({ status: 'loading' });
        const lease = acquireRendition({
            channelId: handle.channelId,
            fileId: handle.fileId,
            revision: handle.revision ?? 0,
            kind: 'thumbnail',
        }, 'visible');
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
                handle.apply({
                    status: 'locked',
                    title: isMobilePlatform() ? 'locked, tap to unlock' : 'locked, click to unlock',
                });
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

    function onIntersect(entries: IntersectionObserverEntry[]): void {
        for (const entry of entries) {
            const handle = handles.get(entry.target as HTMLElement);
            if (!handle) continue;
            if (entry.isIntersecting) {
                if (handle.status === 'idle') void load(handle);
            } else {
                reset(handle);
            }
        }
    }

    function onVisibility(): void {
        for (const handle of handles.values()) {
            if (document.hidden) {
                reset(handle);
            } else {
                observer?.unobserve(handle.node);
                observe(handle);
            }
        }
    }

    function setRoot(nextRoot: HTMLElement): void {
        if (root === nextRoot) return;
        observer?.disconnect();
        root = nextRoot;
        observer = typeof IntersectionObserver === 'undefined'
            ? null
            : new IntersectionObserver(onIntersect, {
                root: nextRoot,
                rootMargin: options.rootMargin ?? '160px 0px',
            });
        for (const handle of handles.values()) observe(handle);
        if (!watchingVisibility) {
            document.addEventListener('visibilitychange', onVisibility);
            watchingVisibility = true;
        }
        unsubscribeReset ??= subscribeRenditionReset(() => {
            for (const handle of handles.values()) {
                reset(handle);
                observer?.unobserve(handle.node);
                if (!document.hidden) observe(handle);
            }
        });
    }

    function teardown(): void {
        observer?.disconnect();
        observer = null;
        root = null;
        if (watchingVisibility) {
            document.removeEventListener('visibilitychange', onVisibility);
            watchingVisibility = false;
        }
        unsubscribeReset?.();
        unsubscribeReset = null;
        for (const handle of handles.values()) release(handle);
        handles.clear();
        currentChannelId = 0;
        active = true;
    }

    function setActive(next: boolean): void {
        if (active === next) return;
        active = next;
        for (const handle of handles.values()) {
            observer?.unobserve(handle.node);
            if (active) observe(handle);
            else reset(handle);
        }
    }

    function beginRender(channelId: number): void {
        if (channelId === currentChannelId) return;
        currentChannelId = channelId;
        for (const handle of handles.values()) {
            observer?.unobserve(handle.node);
            reset(handle);
            observe(handle);
        }
    }

    function unregister(node: HTMLElement): void {
        observer?.unobserve(node);
        const handle = handles.get(node);
        if (handle) release(handle);
        handles.delete(node);
    }

    function register(node: HTMLElement, registration: ThumbnailRegistration): void {
        unregister(node);
        const handle: ThumbnailHandle = {
            ...registration,
            node,
            channelId: registration.channelId ?? currentChannelId,
            status: 'idle',
            src: '',
            attempt: 0,
            retryTimer: 0,
            lease: null,
        };
        handles.set(node, handle);
        observe(handle);
    }

    function rearmLocked(): void {
        for (const handle of handles.values()) {
            if (handle.status !== 'locked') continue;
            handle.attempt = 0;
            release(handle);
            handle.status = 'idle';
            handle.apply({ status: 'idle', title: '' });
            observer?.unobserve(handle.node);
            observe(handle);
        }
    }

    function rearmMissing(fileId?: number): void {
        for (const handle of handles.values()) {
            if (fileId !== undefined && handle.fileId !== fileId) continue;
            if (handle.status !== 'missing' && !(fileId !== undefined && handle.status === 'loading')) continue;
            release(handle);
            handle.status = 'idle';
            handle.apply({ status: 'idle', title: '' });
            observer?.unobserve(handle.node);
            observe(handle);
        }
    }

    function cached(channelId: number, fileId: number): string {
        for (const handle of handles.values()) {
            if (handle.channelId === channelId && handle.fileId === fileId && handle.status === 'loaded') return handle.src;
        }
        return '';
    }

    return {
        setRoot,
        teardown,
        setActive,
        beginRender,
        register,
        unregister,
        rearmLocked,
        rearmMissing,
        cached,
    };
}

export function retryDelay(error: unknown, attempt: number): number | null {
    if (attempt >= 3) return null;
    const advised = Number((error as { retryAfterMs?: number } | null)?.retryAfterMs ?? 0);
    if (Number.isFinite(advised) && advised > 0) return advised + 1000;
    const message = String(error);
    if (!/flood|rate.?limit|timeout/i.test(message)) return null;
    const wait = /wait[:_\s]*(\d+)/i.exec(message);
    return wait ? Number(wait[1]) * 1000 + 1000 : Math.min(90_000, 8_000 * 2 ** attempt);
}
