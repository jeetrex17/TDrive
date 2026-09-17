import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { CellPatch } from './gallery-controller';
import type { RenditionAsset } from '../../modules/renditions/broker';

const runtime = vi.hoisted(() => ({ acquire: vi.fn(), release: vi.fn(), reset: () => {} }));
vi.mock('../../api', () => ({ isMobilePlatform: () => false }));
vi.mock('../../modules/renditions/runtime', () => ({ acquireRendition: runtime.acquire, subscribeRenditionReset: (callback: () => void) => { runtime.reset = callback; return () => {}; } }));
import * as controller from './gallery-controller';

let fire: (node: Element, visible?: boolean) => void;
const observed = new Set<Element>();
class TestObserver {
    constructor(callback: IntersectionObserverCallback) {
        fire = (node, visible = true) => callback([{ target: node, isIntersecting: visible } as IntersectionObserverEntry], this as unknown as IntersectionObserver);
    }
    observe(node: Element) { observed.add(node); }
    unobserve(node: Element) { observed.delete(node); }
    disconnect() { observed.clear(); }
}
function cell(msgId = 10, revision = 1) {
    const node = document.createElement('button');
    const patches: CellPatch[] = [];
    controller.registerCell(node, { msgId, revision, apply: (patch) => patches.push(patch) });
    return { node, patches, last: () => patches[patches.length - 1] };
}
async function flush() { await Promise.resolve(); await Promise.resolve(); }

beforeEach(() => {
    vi.stubGlobal('IntersectionObserver', TestObserver);
    runtime.acquire.mockReset().mockImplementation(() => ({ promise: Promise.resolve({ url: 'blob:10', width: 256, height: 256 }), release: runtime.release }));
    runtime.release.mockReset();
    controller.setRoot(document.createElement('div'));
    controller.beginRender(1);
});
afterEach(() => { controller.teardown(); vi.useRealTimers(); vi.unstubAllGlobals(); });

describe('gallery image leases', () => {
    it('releases hidden Photos and resumes only its mounted visible window', async () => {
        const target = cell(); fire(target.node); await flush();
        controller.setActive(false);
        expect(target.last()).toEqual({ status: 'idle', src: '', title: '' });
        fire(target.node); await flush();
        expect(runtime.acquire).toHaveBeenCalledOnce();
        controller.setActive(true);
        fire(target.node); await flush();
        expect(runtime.acquire).toHaveBeenCalledTimes(2);
    });
    it('clears decoded references while backgrounded and rearms on foreground', async () => {
        const target = cell();
        fire(target.node); await flush();
        vi.spyOn(document, 'hidden', 'get').mockReturnValue(true);
        document.dispatchEvent(new Event('visibilitychange'));
        expect(target.last()).toEqual({ status: 'idle', src: '', title: '' });
        fire(target.node); await flush();
        expect(runtime.acquire).toHaveBeenCalledOnce();
        vi.spyOn(document, 'hidden', 'get').mockReturnValue(false);
        document.dispatchEvent(new Event('visibilitychange'));
        fire(target.node); await flush();
        expect(runtime.acquire).toHaveBeenCalledTimes(2);
        vi.restoreAllMocks();
    });

    it('keeps permanent errors honest and refreshes missing previews after preparation', async () => {
        runtime.acquire.mockImplementationOnce(() => ({ promise: Promise.reject(new Error('unsupported image')), release: runtime.release }));
        const failed = cell(); fire(failed.node); await flush();
        expect(failed.last()?.status).toBe('failed');
        runtime.acquire.mockImplementationOnce(() => ({ promise: Promise.reject({ code: 'missing_rendition' }), release: runtime.release }));
        const missing = cell(11); fire(missing.node); await flush();
        controller.rearmMissing();
        expect(missing.last()).toEqual({ status: 'idle', title: '' });
        fire(missing.node); await flush();
        expect(missing.last()?.status).toBe('loaded');
    });

    it('loads the bounded mounted window on hosts without IntersectionObserver', async () => {
        controller.teardown();
        vi.stubGlobal('IntersectionObserver', undefined);
        controller.setRoot(document.createElement('div'));
        const target = cell(); await flush();
        expect(target.last()?.status).toBe('loaded');
    });
    it('loads visible cells with exact content identity and exposes an active placeholder', async () => {
        const target = cell(10, 7);
        expect(runtime.acquire).not.toHaveBeenCalled();
        fire(target.node);
        await flush();
        expect(runtime.acquire).toHaveBeenCalledWith({ channelId: 1, fileId: 10, revision: 7, kind: 'thumbnail' }, 'visible');
        expect(target.last()).toEqual({ status: 'loaded', src: 'blob:10', title: '' });
        expect(controller.cachedThumb(1, 10)).toBe('blob:10');
        expect(controller.cachedThumb(2, 10)).toBe('');
        controller.unregisterCell(target.node);
        expect(runtime.release).toHaveBeenCalledOnce();
        expect(controller.cachedThumb(1, 10)).toBe('');
    });

    it('releases an image outside the viewport buffer, then reacquires on return', async () => {
        const target = cell();
        fire(target.node); await flush();
        fire(target.node, false);
        expect(target.last()).toEqual({ status: 'idle', src: '', title: '' });
        expect(runtime.release).toHaveBeenCalledOnce();
        fire(target.node); await flush();
        expect(runtime.acquire).toHaveBeenCalledTimes(2);
    });

    it.each(['unregister', 'drive', 'replacement'] as const)('discards late responses after %s', async (action) => {
        let resolve!: (asset: RenditionAsset) => void;
        runtime.acquire.mockImplementation(() => ({ promise: new Promise((done) => { resolve = done; }), release: runtime.release }));
        const target = cell();
        fire(target.node);
        if (action === 'unregister') controller.unregisterCell(target.node);
        if (action === 'drive') controller.beginRender(2);
        if (action === 'replacement') controller.registerCell(target.node, { msgId: 20, apply: () => {} });
        resolve({ url: 'blob:stale', width: 256, height: 256 });
        await flush();
        expect(target.patches.some((patch) => patch.status === 'loaded')).toBe(false);
        expect(runtime.release).toHaveBeenCalledOnce();
    });

    it('renders locked and missing derivatives without original downloads', async () => {
        runtime.acquire.mockImplementationOnce(() => ({ promise: Promise.reject({ code: 'encryption_password_required' }), release: runtime.release }));
        const locked = cell();
        fire(locked.node); await flush();
        expect(locked.last()).toEqual({ status: 'locked', title: 'locked, click to unlock' });
        controller.rearmLocked();
        expect(locked.last()).toEqual({ status: 'idle', title: '' });
        fire(locked.node); await flush();
        expect(locked.last()?.status).toBe('loaded');
        runtime.acquire.mockImplementationOnce(() => ({ promise: Promise.reject({ code: 'missing_rendition' }), release: runtime.release }));
        const missing = cell(20);
        fire(missing.node); await flush();
        expect(missing.last()).toEqual({ status: 'missing', title: 'preview not available yet' });
    });

    it('respects long server deadlines and cancels retries when the cell leaves', async () => {
        vi.useFakeTimers();
        runtime.acquire.mockImplementation(() => ({ promise: Promise.reject({ retryAfterMs: 300_000 }), release: runtime.release }));
        const target = cell();
        fire(target.node); await flush();
        await vi.advanceTimersByTimeAsync(300_000);
        expect(runtime.acquire).toHaveBeenCalledOnce();
        await vi.advanceTimersByTimeAsync(1000);
        expect(runtime.acquire).toHaveBeenCalledTimes(2);
        controller.unregisterCell(target.node);
        await vi.advanceTimersByTimeAsync(400_000);
        expect(runtime.acquire).toHaveBeenCalledTimes(2);
    });

    it('clears revoked image references when the shared scope is reset', async () => {
        const target = cell();
        fire(target.node); await flush();
        runtime.reset();
        expect(target.last()).toEqual({ status: 'idle', src: '', title: '' });
        expect(observed.has(target.node)).toBe(true);
    });

    it('limits retry attempts without truncating Telegram FLOOD_WAIT', () => {
        expect(controller.retryDelay('FLOOD_WAIT_600', 0)).toBe(601_000);
        expect(controller.retryDelay('timeout', 1)).toBe(16_000);
        expect(controller.retryDelay('timeout', 3)).toBeNull();
        expect(controller.retryDelay('unsupported format', 0)).toBeNull();
    });
});
