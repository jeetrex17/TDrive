/**
 * Playback position hints for the backend's read-ahead planner.
 *
 * The player produces state on every frame, so the hints are rate-limited to one
 * per second and only one is ever in flight: the backend wants a recent position,
 * not every position, and a queue of stale hints would describe where the reader
 * used to be.
 */

import { updateMediaPlayback } from '../../api';
import type { PlayerState } from './player-adapters';

const PLAYBACK_HINT_INTERVAL_MS = 1000;

export interface PlaybackHintContext {
    /** The live session token, or "" when there is nothing to report against. */
    token(): string;
    /** Read at send time, not at schedule time, so a deferred hint is not stale. */
    state(): PlayerState;
}

/**
 * How many seconds are buffered ahead of the playhead. It reads the shared
 * PlayerState.buffered ranges, so it works for both the HTML <video> and native
 * mpv engines.
 */
export function bufferAheadSeconds(state: PlayerState): number {
    const t = state.currentTime;
    for (const range of state.buffered) {
        if (range.start <= t && t <= range.end) {
            return Math.max(0, range.end - t);
        }
    }
    return 0;
}

export class PlaybackHintReporter {
    private timer: number | null = null;
    private inFlight = false;
    private lastSentAt = 0;

    constructor(private readonly ctx: PlaybackHintContext) {}

    schedule(state: PlayerState): void {
        if (!this.ctx.token() || state.duration <= 0) return;
        const dueIn = PLAYBACK_HINT_INTERVAL_MS - (Date.now() - this.lastSentAt);
        if (dueIn <= 0) {
            void this.send(state);
            return;
        }
        if (this.timer != null) return;
        this.timer = window.setTimeout(() => {
            this.timer = null;
            void this.send(this.ctx.state());
        }, dueIn);
    }

    clear(): void {
        if (this.timer == null) return;
        window.clearTimeout(this.timer);
        this.timer = null;
    }

    private async send(state: PlayerState): Promise<void> {
        const token = this.ctx.token();
        if (!token || this.inFlight || state.duration <= 0) return;
        this.inFlight = true;
        this.lastSentAt = Date.now();
        try {
            await updateMediaPlayback({
                token,
                currentTime: state.currentTime,
                duration: state.duration,
                bufferAhead: bufferAheadSeconds(state),
            });
        } catch (err) {
            console.warn('UpdateMediaPlayback failed:', err);
        } finally {
            this.inFlight = false;
        }
    }
}
