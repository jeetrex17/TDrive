import { afterEach, describe, expect, it, vi } from "vitest";
import type {
    MediaOpenResult,
    NativeMediaOpenResult,
    NativeMediaStatePayload,
} from "../../api";
import {
    HtmlVideoAdapter,
    NativeMediaStateRouter,
    NativeMpvAdapter,
    playbackSource,
} from "./player-adapters";

const mocks = vi.hoisted(() => {
    const runtime = {
        listener: null as ((value: unknown) => void) | null,
    };
    return {
        runtime,
        closeMedia: vi.fn(async () => undefined),
        isIOSPlatform: vi.fn(() => false),
        closeNativeMedia: vi.fn(async () => undefined),
        nativeMediaCommand: vi.fn(async () => undefined),
        onRuntimeEvent: vi.fn((_name: string, callback: (value: unknown) => void) => {
            runtime.listener = callback;
            return vi.fn();
        }),
    };
});

vi.mock("../../api", () => mocks);

class FakeVideo extends EventTarget {
    volume = 1;
    muted = false;
    playbackRate = 1;
    currentTime = 0;
    duration = 10;
    paused = true;
    readyState = 0;
    error: MediaError | null = null;
    buffered = { length: 0 } as TimeRanges;

    play(): Promise<void> {
        return Promise.resolve();
    }

    pause(): void {}

    load(): void {}

    removeAttribute(_name: string): void {}
}

function mediaInfo() {
    return {
        channelId: 1,
        fileId: 2,
        revision: 3,
        name: "clip.mp4",
        storedSize: 10,
        plaintextSize: 10,
        encrypted: false,
        multipart: false,
    };
}

function htmlOpened(sessionID = "html-session"): MediaOpenResult {
    return {
        token: sessionID,
        url: `http://127.0.0.1/media/${sessionID}`,
        thumbnailUrl: "",
        hlsUrl: "",
        name: "clip.mp4",
        kind: "video",
        mimeType: "video/mp4",
        supportsRange: true,
        info: mediaInfo(),
    };
}

function nativeOpened(sessionID = "native-session"): NativeMediaOpenResult {
    return {
        token: sessionID,
        thumbnailUrl: "",
        htmlControls: false,
        presentation: "embedded",
        initialState: null,
        name: "clip.mp4",
        info: mediaInfo(),
    };
}

function htmlCallbacks() {
    return {
        mediaError: vi.fn(),
        playbackError: vi.fn(),
        revealChrome: vi.fn(),
        mediaEnded: vi.fn(),
    };
}

function nativeCallbacks() {
    return {
        mediaError: vi.fn(),
        mediaClosed: vi.fn(),
        mediaEnded: vi.fn(),
        dispose: vi.fn(),
    };
}

function nativePayload(token: string, overrides: NativeMediaStatePayload = {}): NativeMediaStatePayload {
    return {
        token,
        paused: true,
        duration: 10,
        current_time: 10,
        ...overrides,
    };
}

afterEach(() => {
    mocks.runtime.listener = null;
    vi.clearAllMocks();
    vi.unstubAllGlobals();
});

describe("HTML video natural completion", () => {
    it("reports ended exactly once and ignores ended events after close", async () => {
        const callbacks = htmlCallbacks();
        const video = new FakeVideo();
        const adapter = new HtmlVideoAdapter(video as unknown as HTMLVideoElement, htmlOpened(), callbacks);

        video.dispatchEvent(new Event("ended"));
        video.dispatchEvent(new Event("ended"));
        expect(callbacks.mediaEnded).toHaveBeenCalledTimes(1);

        await adapter.close();
        video.dispatchEvent(new Event("ended"));
        expect(callbacks.mediaEnded).toHaveBeenCalledTimes(1);
    });

    it("does not classify a failed media element as naturally complete", async () => {
        vi.stubGlobal("HTMLMediaElement", { HAVE_FUTURE_DATA: 3 });
        const callbacks = htmlCallbacks();
        const video = new FakeVideo();
        const adapter = new HtmlVideoAdapter(video as unknown as HTMLVideoElement, htmlOpened(), callbacks);

        video.error = { code: 3 } as MediaError;
        video.dispatchEvent(new Event("error"));
        video.dispatchEvent(new Event("ended"));

        expect(callbacks.mediaError).toHaveBeenCalledWith(3, expect.anything());
        expect(callbacks.mediaEnded).not.toHaveBeenCalled();
        await adapter.close();
    });

    it("ignores an error event that carries no MediaError, such as a failed poster", async () => {
        vi.stubGlobal("HTMLMediaElement", { HAVE_FUTURE_DATA: 3 });
        const callbacks = htmlCallbacks();
        const video = new FakeVideo();
        const adapter = new HtmlVideoAdapter(video as unknown as HTMLVideoElement, htmlOpened(), callbacks);

        video.dispatchEvent(new Event("error"));
        expect(callbacks.mediaError).not.toHaveBeenCalled();

        video.error = { code: 2 } as MediaError;
        video.dispatchEvent(new Event("error"));
        expect(callbacks.mediaError).toHaveBeenCalledTimes(1);
        await adapter.close();
    });
});

describe("native video natural completion", () => {
    it("reports ended and eof terminal payloads once", () => {
        const callbacks = nativeCallbacks();
        const adapter = new NativeMpvAdapter(nativeOpened(), callbacks);
        let currentTime = -1;
        adapter.subscribe((state) => {
            currentTime = state.currentTime;
        });

        adapter.receive(nativePayload(adapter.token, { status: "ended" }));
        adapter.receive(nativePayload(adapter.token, { eof: true, sequence: 2 }));
        adapter.receive(nativePayload(adapter.token, { status: "playing", sequence: 3, current_time: 2, paused: false }));

        expect(callbacks.mediaEnded).toHaveBeenCalledTimes(1);
        expect(currentTime).toBe(2);
        expect(adapter.isTerminal()).toBe(false);
    });

    it("never reports natural completion after failure, closure, disposal, or a stale token", async () => {
        const failedCallbacks = nativeCallbacks();
        const failed = new NativeMpvAdapter(nativeOpened("failed"), failedCallbacks);
        failed.receive(nativePayload(failed.token, { status: "failed" }));
        failed.receive(nativePayload(failed.token, { status: "ended" }));
        expect(failedCallbacks.mediaError).toHaveBeenCalledTimes(1);
        expect(failedCallbacks.mediaEnded).not.toHaveBeenCalled();

        const closedCallbacks = nativeCallbacks();
        const closed = new NativeMpvAdapter(nativeOpened("closed"), closedCallbacks);
        closed.receive(nativePayload(closed.token, { status: "closed" }));
        closed.receive(nativePayload(closed.token, { eof: true }));
        expect(closedCallbacks.mediaClosed).toHaveBeenCalledTimes(1);
        expect(closedCallbacks.mediaEnded).not.toHaveBeenCalled();

        const disposedCallbacks = nativeCallbacks();
        const disposed = new NativeMpvAdapter(nativeOpened("disposed"), disposedCallbacks);
        await disposed.close();
        disposed.receive(nativePayload(disposed.token, { status: "ended" }));
        expect(disposedCallbacks.dispose).toHaveBeenCalledTimes(1);
        expect(disposedCallbacks.mediaEnded).not.toHaveBeenCalled();

        const staleCallbacks = nativeCallbacks();
        const current = new NativeMpvAdapter(nativeOpened("current"), staleCallbacks);
        current.receive(nativePayload("old-session", { status: "ended" }));
        expect(staleCallbacks.mediaEnded).not.toHaveBeenCalled();
    });

    it("rejects stale terminal events at the shared state router", async () => {
        const callbacks = nativeCallbacks();
        const adapter = new NativeMpvAdapter(nativeOpened("current"), callbacks);
        const router = new NativeMediaStateRouter();
        router.bind();
        router.activate(adapter);

        mocks.runtime.listener?.(nativePayload("old-session", { status: "ended" }));
        expect(callbacks.mediaEnded).not.toHaveBeenCalled();

        mocks.runtime.listener?.(nativePayload(adapter.token, { eof: true }));
        expect(callbacks.mediaEnded).toHaveBeenCalledTimes(1);

        await adapter.close();
        router.unbind();
    });
});

describe("playbackSource", () => {
    afterEach(() => {
        mocks.isIOSPlatform.mockReturnValue(false);
    });

    it("loads the file directly when there is no remuxed playlist", () => {
        mocks.isIOSPlatform.mockReturnValue(true);
        const opened = htmlOpened();
        expect(playbackSource(opened)).toBe(opened.url);
    });

    it("loads the remuxed playlist on ios", () => {
        mocks.isIOSPlatform.mockReturnValue(true);
        const opened = { ...htmlOpened(), hlsUrl: "http://127.0.0.1/media/hls/tok/index.m3u8" };
        expect(playbackSource(opened)).toBe(opened.hlsUrl);
    });

    it("leaves every other platform on the original container", () => {
        // Elsewhere the player demuxes Matroska itself, and going through HLS
        // would repackage for nothing and lose byte-range seeking.
        const opened = { ...htmlOpened(), hlsUrl: "http://127.0.0.1/media/hls/tok/index.m3u8" };
        expect(playbackSource(opened)).toBe(opened.url);
    });
});
