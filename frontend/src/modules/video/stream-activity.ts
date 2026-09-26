/**
 * The "Streaming 3.4 MB/s (~2.1x)" line, and the polling behind it.
 *
 * This exists as its own thing because it is the only part of the player that
 * is not driven by the player. Everything else in the video controller reacts
 * to a state event from an adapter; this reacts to a clock, asking the backend
 * how the Telegram reader is doing and turning that into one short phrase that
 * two separate surfaces show -- the title-bar meta line, and the buffering
 * spinner's status text.
 *
 * The phrase is held for a moment after the numbers go quiet
 * (STREAM_ACTIVITY_HOLD_MS). Reads arrive in bursts, so a rate that is honestly
 * reported as zero between two bursts would make the line blink on and off
 * several times a second, which reads as a fault rather than as throughput.
 *
 * Nothing here is on a playback-critical path: it wakes once a second and is
 * stopped outright whenever there is no session or the player is in an error
 * state, so it never competes with decoding or seeking for main-thread time.
 */

import type { MediaStats } from "../../api";

const MEDIA_STATS_POLL_MS = 1000;
const STREAM_ACTIVITY_HOLD_MS = 2000;

export interface StreamActivityContext {
    /** Ask the backend for the current session's throughput. */
    readStats(token: string): Promise<MediaStats>;
    /** The live media session token, or "" when nothing is open. */
    activeToken(): string;
    /** True while the player is showing an error, which stops polling. */
    hasError(): boolean;
    /** The file's size in bytes, for the average-bitrate comparison. */
    mediaBytes(): number;
    /** The media's duration in seconds, for the same comparison. */
    durationSeconds(): number;
    /** Called whenever `label` may have changed, so the surfaces can re-render. */
    changed(): void;
}

/** Bytes per second as a short human phrase, never rounding an active stream down to "0.0 KB/s". */
export function formatStreamRate(bytesPerSecond: number): string {
    const safe = Math.max(0, Number.isFinite(bytesPerSecond) ? bytesPerSecond : 0);
    if (safe < 1024 * 1024) {
        return `${Math.max(0.1, safe / 1024).toFixed(1)} KB/s`;
    }
    return `${(safe / (1024 * 1024)).toFixed(1)} MB/s`;
}

/**
 * How far ahead of real time the download is running, as "(~2.1x)".
 *
 * This is the number that actually answers "will this stall?", which the raw
 * rate does not: 3 MB/s is comfortable for one file and not enough for another.
 * It needs both a size and a duration to compute an average bitrate, so it
 * returns "" rather than a guess whenever either is still unknown -- which is
 * the case for the first moments of every open. The 99x ceiling keeps a
 * cache-speed burst from rendering an absurd number in the title bar.
 */
export function formatStreamMultiplier(
    bytesPerSecond: number,
    mediaBytes: number,
    durationSeconds: number,
): string {
    if (!(mediaBytes > 0 && durationSeconds > 0 && bytesPerSecond > 0)) return "";
    const averageBytesPerSecond = mediaBytes / durationSeconds;
    if (!(averageBytesPerSecond > 0)) return "";
    const multiplier = bytesPerSecond / averageBytesPerSecond;
    if (!Number.isFinite(multiplier) || multiplier <= 0) return "";
    if (multiplier >= 100) return "(~99x+)";
    if (multiplier < 10) return `(~${Math.max(0.1, multiplier).toFixed(1)}x)`;
    return `(~${Math.round(multiplier)}x)`;
}

/**
 * The phrase for one stats sample, or "" when there is nothing worth saying.
 *
 * A flood wait outranks the rate: while Telegram is rate-limiting us the rate
 * is near zero anyway, and "Rate-limited" tells the reader the stall is not
 * their connection and not something retrying will fix.
 */
export function streamActivityLabel(
    stats: MediaStats,
    mediaBytes: number,
    durationSeconds: number,
): string {
    const playback = stats.playback;
    if (playback.recentFloodWait) {
        return "Rate-limited";
    }
    const rate = playback.bytesPerSecond || 0;
    if (rate <= 0) return "";
    const multiplier = formatStreamMultiplier(rate, mediaBytes, durationSeconds);
    return `Streaming ${formatStreamRate(rate)}${multiplier ? ` ${multiplier}` : ""}`;
}

/**
 * Polls the backend for stream throughput while a session is open and keeps
 * the current phrase available to whoever renders it.
 *
 * The monitor owns its two timers and nothing else: it never writes to the DOM,
 * it only calls `changed()` and lets the controller decide what that means.
 */
export class StreamActivityMonitor {
    private pollTimer: number | null = null;
    private pollInFlight = false;
    private holdTimer: number | null = null;
    private text = "";
    private textAt = 0;

    constructor(private readonly ctx: StreamActivityContext) {}

    /** The phrase to show right now, or "" for nothing. */
    get label(): string {
        return this.text;
    }

    /**
     * Start or stop polling to match the player's current state. Safe to call
     * on every state event: starting an already-running poll is a no-op.
     */
    sync(): void {
        if (this.ctx.activeToken() && !this.ctx.hasError()) this.start();
        else this.stop();
    }

    /** Stop polling, drop the phrase, and let the surfaces re-render without it. */
    stop(): void {
        if (this.pollTimer != null) {
            window.clearInterval(this.pollTimer);
            this.pollTimer = null;
        }
        this.clearHoldTimer();
        this.text = "";
        this.textAt = 0;
        this.ctx.changed();
    }

    private start(): void {
        if (this.pollTimer != null) return;
        // Poll once immediately: waiting a full second for the first sample
        // leaves the meta line empty over exactly the stretch -- the opening
        // buffer -- where the reader most wants to see something moving.
        void this.poll();
        this.pollTimer = window.setInterval(() => {
            void this.poll();
        }, MEDIA_STATS_POLL_MS);
    }

    private async poll(): Promise<void> {
        const token = this.ctx.activeToken();
        if (!token || this.pollInFlight) return;
        this.pollInFlight = true;
        try {
            const stats = await this.ctx.readStats(token);
            // The session can be closed and reopened while a request is in
            // flight; applying a dead session's numbers to the new one would
            // report the old file's throughput.
            if (token !== this.ctx.activeToken()) return;
            this.apply(stats);
        } catch (err) {
            console.warn("GetMediaStats failed:", err);
        } finally {
            this.pollInFlight = false;
        }
    }

    private apply(stats: MediaStats | null): void {
        const text = stats ? streamActivityLabel(stats, this.ctx.mediaBytes(), this.ctx.durationSeconds()) : "";
        const now = Date.now();
        if (text) {
            this.text = text;
            this.textAt = now;
            this.scheduleHoldExpiry();
        } else if (this.text && now - this.textAt >= STREAM_ACTIVITY_HOLD_MS) {
            this.text = "";
            this.clearHoldTimer();
        }
        this.ctx.changed();
    }

    /**
     * Clear the phrase once the hold has elapsed, without waiting for another
     * poll to notice. A stream that stops entirely also stops producing samples
     * to be nudged by, so without this timer the last phrase would stay on
     * screen until the session closed.
     */
    private scheduleHoldExpiry(): void {
        this.clearHoldTimer();
        this.holdTimer = window.setTimeout(() => {
            if (Date.now() - this.textAt >= STREAM_ACTIVITY_HOLD_MS) {
                this.text = "";
                this.ctx.changed();
            }
            this.holdTimer = null;
        }, STREAM_ACTIVITY_HOLD_MS);
    }

    private clearHoldTimer(): void {
        if (this.holdTimer == null) return;
        window.clearTimeout(this.holdTimer);
        this.holdTimer = null;
    }
}
