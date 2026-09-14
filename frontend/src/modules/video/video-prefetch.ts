import type { PlayerState } from './player-adapters';

/** How close to the end the current video gets before the next one is warmed. */
export const PREFETCH_LEAD_SECONDS = 25;

/**
 * How much of the next file to pull. Enough for the backend to resolve the
 * Telegram file and fill its first read-ahead chunks, which is what makes the
 * switch feel instant; not so much that it competes for bandwidth.
 */
export const PREFETCH_HEAD_BYTES = 2 * 1024 * 1024;

/** Slack allowed when deciding the current file is buffered to its end. */
const BUFFER_TAIL_TOLERANCE_SECONDS = 1.5;

/**
 * readyToPrefetch reports whether the next item is worth warming right now.
 *
 * Both conditions matter. Near the end means the switch is imminent, and fully
 * buffered means the current file no longer needs the connection, so warming
 * the next one cannot stall what is still playing.
 */
export function readyToPrefetch(state: PlayerState): boolean {
    if (state.loading || state.paused) return false;
    if (!Number.isFinite(state.duration) || state.duration <= 0) return false;
    const remaining = state.duration - state.currentTime;
    if (remaining <= 0 || remaining > PREFETCH_LEAD_SECONDS) return false;
    return bufferedToEnd(state);
}

/** bufferedToEnd reports whether the range under the playhead reaches the end. */
export function bufferedToEnd(state: PlayerState): boolean {
    const target = state.duration - BUFFER_TAIL_TOLERANCE_SECONDS;
    return state.buffered.some(
        (range) => range.start <= state.currentTime + BUFFER_TAIL_TOLERANCE_SECONDS && range.end >= target,
    );
}

/** A media session opened ahead of time, held until the player asks for it. */
export interface PrefetchedSession<Result> {
    id: number;
    result: Result;
}

/**
 * MediaPrefetcher holds at most one warmed session. It is deliberately a
 * single slot: only the immediate next item is ever worth keeping open, and a
 * session left open holds a reader on the backend.
 */
export class MediaPrefetcher<Result extends { token: string; url: string }> {
    private pending: number | null = null;
    private session: PrefetchedSession<Result> | null = null;

    constructor(
        private readonly deps: {
            open: (id: number) => Promise<Result>;
            close: (token: string) => Promise<void>;
            warm: (url: string) => Promise<void>;
        },
    ) {}

    /** holds reports whether a session for id is already warmed or on its way. */
    holds(id: number): boolean {
        return this.pending === id || this.session?.id === id;
    }

    /**
     * prepare opens and warms a session for id unless one is already held.
     * Failures are swallowed: a cold switch is still a working switch.
     */
    async prepare(id: number): Promise<void> {
        if (this.holds(id)) return;
        await this.discard();
        this.pending = id;
        try {
            const result = await this.deps.open(id);
            if (this.pending !== id) {
                await this.deps.close(result.token).catch(() => undefined);
                return;
            }
            this.session = { id, result };
            this.pending = null;
            if (result.url) await this.deps.warm(result.url);
        } catch {
            if (this.pending === id) this.pending = null;
        }
    }

    /** take hands over the session for id, if one is ready, and clears the slot. */
    take(id: number): Result | null {
        if (this.session?.id !== id) return null;
        const { result } = this.session;
        this.session = null;
        this.pending = null;
        return result;
    }

    /** discard closes anything held. Safe to call when nothing is. */
    async discard(): Promise<void> {
        this.pending = null;
        const held = this.session;
        this.session = null;
        if (held) await this.deps.close(held.result.token).catch(() => undefined);
    }
}

/**
 * warmMediaHead pulls the first bytes of a loopback URL so the backend has an
 * open reader and a filled chunk before playback asks for anything.
 */
export async function warmMediaHead(url: string, signal?: AbortSignal): Promise<void> {
    const response = await fetch(url, {
        headers: { Range: `bytes=0-${PREFETCH_HEAD_BYTES - 1}` },
        signal,
    });
    // The body has to be drained, or the range reader never actually runs.
    await response.arrayBuffer();
}
