import { describe, expect, it, vi } from 'vitest';
import { EMPTY_PLAYER_STATE, type PlayerState } from './player-adapters';
import {
    MediaPrefetcher,
    PREFETCH_HEAD_BYTES,
    PREFETCH_LEAD_SECONDS,
    PREFETCH_TAIL_BYTES,
    readyToPrefetch,
    warmMediaEdges,
} from './video-prefetch';

function state(overrides: Partial<PlayerState>): PlayerState {
    return { ...EMPTY_PLAYER_STATE, paused: false, duration: 600, currentTime: 590, ...overrides };
}

const bufferedToEnd = [{ start: 0, end: 600 }];

describe('readyToPrefetch', () => {
    it('warms the next item near the end of a fully buffered file', () => {
        expect(readyToPrefetch(state({ buffered: bufferedToEnd }))).toBe(true);
    });

    it('waits while the file is still downloading', () => {
        // The tail is not buffered yet, so warming would compete for bandwidth
        // with the video the viewer is actually watching.
        expect(readyToPrefetch(state({ buffered: [{ start: 0, end: 592 }] }))).toBe(false);
    });

    it('waits until playback is close to the end', () => {
        const early = state({ currentTime: 600 - PREFETCH_LEAD_SECONDS - 1, buffered: bufferedToEnd });
        expect(readyToPrefetch(early)).toBe(false);
    });

    it('stays out of the way while paused, loading, or without a duration', () => {
        expect(readyToPrefetch(state({ buffered: bufferedToEnd, paused: true }))).toBe(false);
        expect(readyToPrefetch(state({ buffered: bufferedToEnd, loading: true }))).toBe(false);
        expect(readyToPrefetch(state({ buffered: bufferedToEnd, duration: 0 }))).toBe(false);
    });
});

describe('MediaPrefetcher', () => {
    const session = (id: number) => ({ token: `token-${id}`, url: `http://127.0.0.1/media/${id}` });

    function harness() {
        const open = vi.fn(async (id: number) => session(id));
        const close = vi.fn(async () => undefined);
        const warm = vi.fn(async () => undefined);
        return { open, close, warm, prefetcher: new MediaPrefetcher({ open, close, warm }) };
    }

    it('opens and warms once, then hands the session over', async () => {
        const { open, warm, prefetcher } = harness();

        await prefetcher.prepare(7);
        await prefetcher.prepare(7);

        expect(open).toHaveBeenCalledTimes(1);
        expect(warm).toHaveBeenCalledWith(session(7));
        expect(prefetcher.take(7)).toEqual(session(7));
        // The slot is empty afterwards, so the player cannot use it twice.
        expect(prefetcher.take(7)).toBeNull();
    });

    it('does not hand over a session opened for a different item', async () => {
        const { prefetcher } = harness();
        await prefetcher.prepare(7);
        expect(prefetcher.take(8)).toBeNull();
    });

    it('closes the previous session when the queue moves on', async () => {
        const { close, prefetcher } = harness();

        await prefetcher.prepare(7);
        await prefetcher.prepare(8);

        expect(close).toHaveBeenCalledWith('token-7');
        expect(prefetcher.take(8)).toEqual(session(8));
    });

    it('closes what it holds on discard', async () => {
        const { close, prefetcher } = harness();
        await prefetcher.prepare(7);
        await prefetcher.discard();
        expect(close).toHaveBeenCalledWith('token-7');
        expect(prefetcher.take(7)).toBeNull();
    });

    it('stays usable when opening fails', async () => {
        const { close } = harness();
        const failing = new MediaPrefetcher<{ token: string; url: string }>({
            open: async () => { throw new Error('offline'); },
            close,
            warm: async () => undefined,
        });

        await expect(failing.prepare(7)).resolves.toBeUndefined();
        expect(failing.holds(7)).toBe(false);
        expect(failing.take(7)).toBeNull();
    });
});

describe('warmMediaEdges', () => {
    function fetchSpy() {
        const ranges: string[] = [];
        const spy = vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
            ranges.push(String((init?.headers as Record<string, string>)?.Range));
            return { arrayBuffer: async () => new ArrayBuffer(8) } as unknown as Response;
        });
        vi.stubGlobal('fetch', spy);
        return { ranges, spy };
    }

    it('pulls the head and the container index at the tail together', async () => {
        const { ranges } = fetchSpy();
        const size = 240 * 1024 * 1024;

        await warmMediaEdges('http://127.0.0.1/media/7', size);

        expect(ranges).toEqual([
            `bytes=0-${PREFETCH_HEAD_BYTES - 1}`,
            `bytes=${size - PREFETCH_TAIL_BYTES}-${size - 1}`,
        ]);
    });

    it('pulls only the head when the file is smaller than both windows', async () => {
        const { ranges } = fetchSpy();
        await warmMediaEdges('http://127.0.0.1/media/7', 512 * 1024);
        expect(ranges).toEqual([`bytes=0-${PREFETCH_HEAD_BYTES - 1}`]);
    });
});
