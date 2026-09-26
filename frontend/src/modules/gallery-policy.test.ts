import { describe, expect, it, vi } from 'vitest';
const events = vi.hoisted(() => ({ listeners: new Map<string, (payload: unknown) => void>(), cleanup: vi.fn() }));
vi.mock('../api/runtime', () => ({ isMobilePlatform: () => true, onRuntimeEvent: vi.fn((name: string, callback: (payload: unknown) => void) => { events.listeners.set(name, callback); return events.cleanup; }) }));
import { acquireOriginalViewerBudget, deriveGalleryPolicy, normalizeGallerySignals, getGalleryPolicy, subscribeGalleryPolicy, updateGallerySignals } from './gallery-policy';

describe('gallery resource policy', () => {
    it('never prefetches while network cost is unknown', () => {
        expect(deriveGalleryPolicy(true, {}).allowPrefetch).toBe(false);
        expect(deriveGalleryPolicy(false, {}).allowPrefetch).toBe(false);
    });
    it('allows one next preview only with explicitly unconstrained access', () => {
        expect(deriveGalleryPolicy(true, { connected: true, metered: false }).allowPrefetch).toBe(true);
        for (const signal of [{ metered: true }, { constrained: true }, { lowPowerMode: true }, { backgrounded: true }, { connected: false }, { memoryPressure: true }]) {
            expect(deriveGalleryPolicy(true, { connected: true, metered: false, ...signal }).allowPrefetch).toBe(false);
        }
    });
    it('separates phone and desktop budgets and pauses background requests', () => {
        expect(deriveGalleryPolicy(true, {}).decodedBytes).toBe(80 * 1024 * 1024);
        expect(deriveGalleryPolicy(false, {}).decodedBytes).toBe(192 * 1024 * 1024);
        expect(deriveGalleryPolicy(true, { backgrounded: true }).concurrency).toBe(0);
        expect(deriveGalleryPolicy(false, { lowPowerMode: true }).concurrency).toBe(1);
    });
    it('reserves thumbnail memory while an original image viewer is open', () => {
        const listener = vi.fn();
        const unsubscribe = subscribeGalleryPolicy(listener);
        const release = acquireOriginalViewerBudget();
        expect(getGalleryPolicy()).toMatchObject({ decodedBytes: 16 * 1024 * 1024, compressedBytes: 2 * 1024 * 1024 });
        expect(listener).toHaveBeenLastCalledWith(expect.objectContaining({ decodedBytes: 16 * 1024 * 1024 }));
        release();
        expect(getGalleryPolicy()).toMatchObject({ decodedBytes: 80 * 1024 * 1024, compressedBytes: 8 * 1024 * 1024 });
        const calls = listener.mock.calls.length;
        release();
        expect(listener).toHaveBeenCalledTimes(calls);
        unsubscribe();
        events.cleanup.mockClear();
    });
    it('shares native signal listeners and tears them down with the last subscriber', () => {
        const first = vi.fn();
        const second = vi.fn();
        const releaseFirst = subscribeGalleryPolicy(first);
        const releaseSecond = subscribeGalleryPolicy(second);
        events.listeners.get('ios:NetworkChanged')!({ connected: true, expensive: false, constrained: false });
        expect(first).toHaveBeenCalledWith(expect.objectContaining({ allowPrefetch: true }));
        expect(second).toHaveBeenCalledWith(expect.objectContaining({ allowPrefetch: true }));
        releaseFirst();
        updateGallerySignals({ lowPowerMode: true });
        expect(getGalleryPolicy().allowPrefetch).toBe(false);
        expect(first).toHaveBeenCalledTimes(1);
        expect(second).toHaveBeenCalledTimes(2);
        releaseSecond();
        expect(events.cleanup).toHaveBeenCalledTimes(5);
    });

    it('reduces budgets during pressure and restarts the recovery deadline', () => {
        vi.useFakeTimers();
        updateGallerySignals({ lowPowerMode: false, metered: false, connected: true });
        const listener = vi.fn();
        const unsubscribe = subscribeGalleryPolicy(listener);
        events.listeners.get('gallery_memory_pressure')!({});
        expect(getGalleryPolicy()).toMatchObject({ concurrency: 1, decodedBytes: 48 * 1024 * 1024, compressedBytes: 4 * 1024 * 1024, allowPrefetch: false });
        vi.advanceTimersByTime(30_000);
        events.listeners.get('gallery_memory_pressure')!({});
        vi.advanceTimersByTime(30_001);
        expect(getGalleryPolicy().allowPrefetch).toBe(false);
        vi.advanceTimersByTime(30_000);
        expect(getGalleryPolicy()).toMatchObject({ concurrency: 2, allowPrefetch: true });
        events.listeners.get('gallery_memory_pressure')!({});
        unsubscribe();
        expect(vi.getTimerCount()).toBe(0);
        vi.advanceTimersByTime(60_001);
        vi.useRealTimers();
    });

    it('ignores malformed native values and supports JSON payloads', () => {
        expect(normalizeGallerySignals('{"metered":false,"connected":true}')).toEqual({ metered: false, connected: true });
        expect(normalizeGallerySignals({ metered: 'false', connected: 1 })).toEqual({});
        expect(normalizeGallerySignals('invalid')).toEqual({});
        expect(normalizeGallerySignals(null)).toEqual({});
    });
});
