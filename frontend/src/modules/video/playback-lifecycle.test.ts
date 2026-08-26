import { describe, expect, it, vi } from "vitest";
import {
    HTML_MEDIA_ERROR,
    SerialPlaybackTransitions,
    capturePlaybackIntent,
    shouldFallbackFromHtmlMediaError,
} from "./playback-lifecycle";

describe("HTML media fallback", () => {
    it("falls back only for decoder and unsupported-source errors", () => {
        expect(shouldFallbackFromHtmlMediaError(HTML_MEDIA_ERROR.decode)).toBe(true);
        expect(shouldFallbackFromHtmlMediaError(HTML_MEDIA_ERROR.sourceNotSupported)).toBe(true);
        expect(shouldFallbackFromHtmlMediaError(HTML_MEDIA_ERROR.aborted)).toBe(false);
        expect(shouldFallbackFromHtmlMediaError(HTML_MEDIA_ERROR.network)).toBe(false);
        expect(shouldFallbackFromHtmlMediaError(undefined)).toBe(false);
    });

    it("captures a finite, immutable playback intent", () => {
        const state = {
            paused: false,
            currentTime: 42.5,
            volume: 0.35,
            muted: true,
            rate: 1.5,
        };

        const intent = capturePlaybackIntent(state, true);
        state.currentTime = 99;

        expect(intent).toEqual({
            paused: true,
            currentTime: 42.5,
            volume: 0.35,
            muted: true,
            rate: 1.5,
        });
    });

    it("sanitizes invalid media state before native restoration", () => {
        expect(capturePlaybackIntent({
            paused: false,
            currentTime: Number.NaN,
            volume: Number.POSITIVE_INFINITY,
            muted: false,
            rate: 99,
        }, false)).toEqual({
            paused: false,
            currentTime: 0,
            volume: 1,
            muted: false,
            rate: 4,
        });
    });
});

describe("SerialPlaybackTransitions", () => {
    it("serializes close-before-open work", async () => {
        const transitions = new SerialPlaybackTransitions();
        const generation = transitions.begin();
        const order: string[] = [];
        let finishClose: (() => void) | undefined;
        const closeFinished = new Promise<void>((resolve) => {
            finishClose = resolve;
        });

        const close = transitions.run(generation, async () => {
            order.push("close:start");
            await closeFinished;
            order.push("close:end");
        });
        const open = transitions.run(generation, async () => {
            order.push("open");
        });

        await Promise.resolve();
        expect(order).toEqual(["close:start"]);
        finishClose?.();
        await Promise.all([close, open]);
        expect(order).toEqual(["close:start", "close:end", "open"]);
    });

    it("skips queued stale work and exposes cancellation after awaits", async () => {
        const transitions = new SerialPlaybackTransitions();
        const firstGeneration = transitions.begin();
        let releaseFirst: (() => void) | undefined;
        const firstWait = new Promise<void>((resolve) => {
            releaseFirst = resolve;
        });
        const staleQueuedWork = vi.fn();
        const checks: boolean[] = [];

        const first = transitions.run(firstGeneration, async (isCurrent) => {
            checks.push(isCurrent());
            await firstWait;
            checks.push(isCurrent());
        });
        const skipped = transitions.run(firstGeneration, staleQueuedWork);

        await Promise.resolve();
        const secondGeneration = transitions.begin();
        releaseFirst?.();
        await Promise.all([first, skipped]);

        expect(checks).toEqual([true, false]);
        expect(staleQueuedWork).not.toHaveBeenCalled();
        expect(transitions.isCurrent(secondGeneration)).toBe(true);
        expect(transitions.isCurrent(firstGeneration)).toBe(false);
    });

    it("keeps the queue usable after a failed transition", async () => {
        const transitions = new SerialPlaybackTransitions();
        const generation = transitions.begin();
        const afterFailure = vi.fn();

        await expect(transitions.run(generation, async () => {
            throw new Error("open failed");
        })).rejects.toThrow("open failed");

        await transitions.run(generation, afterFailure);
        expect(afterFailure).toHaveBeenCalledOnce();
    });
});
