import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { flushSync } from "svelte";

const apiMocks = vi.hoisted(() => ({
    attachNativeMedia: vi.fn(),
    closeMedia: vi.fn(),
    closeNativeMedia: vi.fn(),
    getMediaStats: vi.fn(),
    hideNativeSeekThumbnail: vi.fn(),
    moveNativeSeekThumbnail: vi.fn(),
    nativeMediaCommand: vi.fn(),
    openMedia: vi.fn(),
    openNativeMedia: vi.fn(),
    resizeNativeMedia: vi.fn(),
    showNativeSeekThumbnail: vi.fn(),
    updateMediaPlayback: vi.fn(),
}));

const runtimeMocks = vi.hoisted(() => ({
    events: new Map<string, (payload: unknown) => void>(),
    eventsOn: vi.fn(),
}));

const SHARED_SESSION_ID = "shared-session-id";
const MKV_SESSION_ID = "mkv-session-id";
const OLD_MKV_SESSION_ID = "old-mkv-session-id";
const FAILED_NATIVE_SESSION_ID = "failed-native-session-id";

vi.mock("../../api", () => apiMocks);
vi.mock("../../../wailsjs/runtime/runtime", () => ({
    EventsOn: (name: string, callback: (payload: unknown) => void) => {
        runtimeMocks.eventsOn(name, callback);
        runtimeMocks.events.set(name, callback);
        return () => runtimeMocks.events.delete(name);
    },
    WindowFullscreen: vi.fn(),
    WindowIsFullscreen: vi.fn(async () => false),
    WindowUnfullscreen: vi.fn(),
}));

const paused = new WeakMap<HTMLMediaElement, boolean>();

function mediaOpenResult(id: number, token: string) {
    return {
        token,
        url: `http://127.0.0.1/media/file/${token}`,
        thumbnailUrl: "",
        name: `clip-${id}.mp4`,
        kind: "video",
        mimeType: "video/mp4",
        supportsRange: true,
        info: {
            channelId: 1,
            fileId: id,
            name: `clip-${id}.mp4`,
            storedSize: 1024,
            plaintextSize: 1024,
            encrypted: false,
            multipart: false,
        },
    };
}

function nativeOpenResult(id: number, token: string) {
    const opened = mediaOpenResult(id, token);
    return {
        ...opened,
        name: `movie-${id}.mkv`,
        htmlControls: false,
        presentation: "embedded",
        initialState: {
            token,
            sequence: 1,
            status: "opening",
            paused: true,
            loading: true,
            volume: 1,
            rate: 1,
        },
        info: { ...opened.info, name: `movie-${id}.mkv` },
    };
}

function installMediaElementStubs(): void {
    vi.spyOn(HTMLMediaElement.prototype, "load").mockImplementation(() => undefined);
    vi.spyOn(HTMLMediaElement.prototype, "pause").mockImplementation(function (this: HTMLMediaElement) {
        paused.set(this, true);
        this.dispatchEvent(new Event("pause"));
    });
    vi.spyOn(HTMLMediaElement.prototype, "play").mockImplementation(function (this: HTMLMediaElement) {
        paused.set(this, false);
        this.dispatchEvent(new Event("playing"));
        return Promise.resolve();
    });
    vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockReturnValue({
        x: 0,
        y: 0,
        top: 0,
        right: 800,
        bottom: 450,
        left: 0,
        width: 800,
        height: 450,
        toJSON: () => ({}),
    });
    Object.defineProperty(HTMLMediaElement.prototype, "paused", {
        configurable: true,
        get() {
            return paused.get(this) ?? true;
        },
    });
}

async function nextTasks(): Promise<void> {
    await Promise.resolve();
    await new Promise((resolve) => setTimeout(resolve, 0));
    flushSync();
}

beforeEach(() => {
    vi.resetModules();
    vi.clearAllMocks();
    runtimeMocks.events.clear();
    document.body.innerHTML = '<div id="file-list" tabindex="-1"></div><div id="video-modal" style="display:none"></div>';
    installMediaElementStubs();
    vi.stubGlobal("requestAnimationFrame", (callback: FrameRequestCallback) => window.setTimeout(() => callback(0), 0));
    vi.stubGlobal("cancelAnimationFrame", (id: number) => window.clearTimeout(id));
    apiMocks.closeMedia.mockResolvedValue(undefined);
    apiMocks.closeNativeMedia.mockResolvedValue(undefined);
    apiMocks.getMediaStats.mockResolvedValue({ playback: {}, thumbnails: {} });
    apiMocks.hideNativeSeekThumbnail.mockResolvedValue(undefined);
    apiMocks.moveNativeSeekThumbnail.mockResolvedValue(undefined);
    apiMocks.nativeMediaCommand.mockResolvedValue(undefined);
    apiMocks.resizeNativeMedia.mockResolvedValue(undefined);
    apiMocks.showNativeSeekThumbnail.mockResolvedValue(undefined);
    apiMocks.updateMediaPlayback.mockResolvedValue(undefined);
});

afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
    document.body.replaceChildren();
});

describe("video HTML-to-native fallback", () => {
    it("promotes one token once and restores playback intent", async () => {
        const opened = mediaOpenResult(7, SHARED_SESSION_ID);
        opened.info.encrypted = true;
        apiMocks.openMedia.mockResolvedValue(opened);
        apiMocks.attachNativeMedia.mockResolvedValue({
            token: opened.token,
            thumbnailUrl: "",
            htmlControls: true,
            name: opened.name,
            info: opened.info,
        });

        const videoModule = await import("./video");
        videoModule.setupVideoModal();
        await videoModule.openVideoModal({ id: 7, name: "clip-7.mp4", size: 1024, encrypted: true });

        const video = document.querySelector<HTMLVideoElement>("#video-player");
        expect(video).not.toBeNull();
        if (!video) return;

        video.currentTime = 42.5;
        video.volume = 0.35;
        video.muted = true;
        video.playbackRate = 1.5;
        video.dispatchEvent(new Event("timeupdate"));
        video.dispatchEvent(new Event("volumechange"));
        video.dispatchEvent(new Event("ratechange"));

        document.querySelector<HTMLButtonElement>("#video-play")?.click();
        Object.defineProperty(video, "error", {
            configurable: true,
            value: { code: 3, message: "decode failed" },
        });
        video.dispatchEvent(new Event("error"));
        video.dispatchEvent(new Event("error"));

        await vi.waitFor(() => expect(apiMocks.attachNativeMedia).toHaveBeenCalledOnce());
        expect(apiMocks.openMedia).toHaveBeenCalledOnce();
        expect(apiMocks.openNativeMedia).not.toHaveBeenCalled();
        expect(apiMocks.attachNativeMedia).toHaveBeenCalledWith(SHARED_SESSION_ID, expect.objectContaining({ width: 800, height: 450 }));
        expect(apiMocks.closeMedia).not.toHaveBeenCalled();
        expect(document.querySelector("#video-loading-status")?.textContent).toContain("compatible player");

        runtimeMocks.events.get("native_media_state")?.({
            token: SHARED_SESSION_ID,
            paused: true,
            current_time: 0,
            duration: 120,
            volume: 0.35,
            muted: true,
            rate: 1.5,
            loading: true,
        });
        await nextTasks();

        expect(apiMocks.nativeMediaCommand).toHaveBeenCalledWith(SHARED_SESSION_ID, ["cycle", "pause"]);
        expect(apiMocks.nativeMediaCommand).toHaveBeenCalledWith(SHARED_SESSION_ID, ["set", "volume", "35"]);
        expect(apiMocks.nativeMediaCommand).toHaveBeenCalledWith(SHARED_SESSION_ID, ["set", "mute", "yes"]);
        expect(apiMocks.nativeMediaCommand).toHaveBeenCalledWith(SHARED_SESSION_ID, ["set", "speed", "1.5"]);
        expect(apiMocks.nativeMediaCommand).toHaveBeenCalledWith(SHARED_SESSION_ID, ["seek", "42.5", "absolute"]);

        await videoModule.closeVideoModal();
        expect(apiMocks.closeNativeMedia).toHaveBeenCalledOnce();
        expect(apiMocks.closeNativeMedia).toHaveBeenCalledWith(SHARED_SESSION_ID);
        expect(apiMocks.closeMedia).not.toHaveBeenCalled();
    });

    it("closes the shared HTML token when native attachment fails", async () => {
        const opened = mediaOpenResult(8, "failed-attach-token");
        apiMocks.openMedia.mockResolvedValue(opened);
        apiMocks.attachNativeMedia.mockRejectedValue(new Error("native renderer unavailable"));

        const videoModule = await import("./video");
        videoModule.setupVideoModal();
        await videoModule.openVideoModal({ id: 8, name: "clip-8.mp4", size: 1024 });

        const video = document.querySelector<HTMLVideoElement>("#video-player");
        expect(video).not.toBeNull();
        if (!video) return;
        Object.defineProperty(video, "error", {
            configurable: true,
            value: { code: 4, message: "source not supported" },
        });
        video.dispatchEvent(new Event("error"));
        video.dispatchEvent(new Event("error"));

        await vi.waitFor(() => expect(apiMocks.closeMedia).toHaveBeenCalledWith("failed-attach-token"));
        expect(apiMocks.attachNativeMedia).toHaveBeenCalledOnce();
        expect(apiMocks.closeMedia).toHaveBeenCalledOnce();
        expect(apiMocks.closeNativeMedia).not.toHaveBeenCalled();
        expect(document.querySelector("#video-error")?.textContent).toContain("native renderer unavailable");
    });

    it("closes a stale promoted player before opening the newer request", async () => {
        const first = mediaOpenResult(9, "stale-token");
        const second = mediaOpenResult(10, "current-token");
        apiMocks.openMedia.mockResolvedValueOnce(first).mockResolvedValueOnce(second);
        let finishAttach: ((opened: ReturnType<typeof mediaOpenResult> & { htmlControls: boolean }) => void) | undefined;
        apiMocks.attachNativeMedia.mockImplementation(() => new Promise((resolve) => {
            finishAttach = resolve;
        }));

        const videoModule = await import("./video");
        videoModule.setupVideoModal();
        await videoModule.openVideoModal({ id: 9, name: "clip-9.mp4", size: 1024 });

        const video = document.querySelector<HTMLVideoElement>("#video-player");
        expect(video).not.toBeNull();
        if (!video) return;
        Object.defineProperty(video, "error", {
            configurable: true,
            value: { code: 3, message: "decode failed" },
        });
        video.dispatchEvent(new Event("error"));
        await vi.waitFor(() => expect(apiMocks.attachNativeMedia).toHaveBeenCalledOnce());

        const newerOpen = videoModule.openVideoModal({ id: 10, name: "clip-10.mp4", size: 1024 });
        finishAttach?.({ ...first, htmlControls: true });
        await newerOpen;

        expect(apiMocks.closeNativeMedia).toHaveBeenCalledWith("stale-token");
        expect(apiMocks.openMedia).toHaveBeenCalledTimes(2);
        expect(apiMocks.openMedia).toHaveBeenLastCalledWith(10);
        expect(runtimeMocks.events.has("native_media_state")).toBe(true);
        expect(document.querySelector("#video-filename")?.textContent).toBe("clip-10.mp4");

        await videoModule.closeVideoModal();
        expect(apiMocks.closeMedia).toHaveBeenCalledWith("current-token");
    });
});

describe("native video track controls", () => {
    it("shows useful audio and subtitle choices and sends validated mpv commands", async () => {
        apiMocks.openNativeMedia.mockResolvedValue(nativeOpenResult(20, MKV_SESSION_ID));

        const videoModule = await import("./video");
        videoModule.setupVideoModal();
        videoModule.setupVideoModal();
        await videoModule.openVideoModal({ id: 20, name: "movie-20.mkv", size: 1024 });

        runtimeMocks.events.get("native_media_state")?.({
            token: MKV_SESSION_ID,
            paused: false,
            duration: 240,
            tracks: [
                { id: 1, type: "audio", title: "Main", language: "eng", codec: "aac", selected: true, default: true, forced: false },
                { id: 2, type: "audio", title: "Commentary", language: "eng", codec: "opus", selected: false, default: false, forced: false },
                { id: 3, type: "subtitle", title: "English SDH", language: "eng", codec: "ass", selected: false, default: false, forced: false },
            ],
        });
        await nextTasks();

        const controls = document.querySelector<HTMLElement>("#video-track-controls");
        const audioControl = document.querySelector<HTMLElement>("#video-audio-control");
        const subtitleControl = document.querySelector<HTMLElement>("#video-subtitle-control");
        const audio = document.querySelector<HTMLSelectElement>("#video-audio-select");
        const subtitles = document.querySelector<HTMLSelectElement>("#video-subtitle-select");

        expect(controls?.hidden).toBe(false);
        expect(audioControl?.hidden).toBe(false);
        expect(subtitleControl?.hidden).toBe(false);
        expect(Array.from(audio?.options ?? []).map((option) => option.textContent)).toEqual([
            "Main / ENG / AAC",
            "Commentary / ENG / OPUS",
        ]);
        expect(Array.from(subtitles?.options ?? []).map((option) => option.textContent)).toEqual([
            "Off",
            "English SDH / ENG / ASS",
        ]);
        expect(audio?.value).toBe("1");
        expect(subtitles?.value).toBe("no");

        if (!audio || !subtitles) return;
        audio.value = "2";
        audio.dispatchEvent(new Event("change"));
        subtitles.value = "3";
        subtitles.dispatchEvent(new Event("change"));

        await vi.waitFor(() => {
            expect(apiMocks.nativeMediaCommand).toHaveBeenCalledWith(MKV_SESSION_ID, ["set", "aid", "2"]);
            expect(apiMocks.nativeMediaCommand).toHaveBeenCalledWith(MKV_SESSION_ID, ["set", "sid", "3"]);
        });
        expect(apiMocks.nativeMediaCommand.mock.calls.filter(([, command]) => (
            command[0] === "set" && command[1] === "aid" && command[2] === "2"
        ))).toHaveLength(1);

        runtimeMocks.events.get("native_media_state")?.({
            token: MKV_SESSION_ID,
            tracks: [
                { id: 1, type: "audio", title: "Main", language: "eng", codec: "aac", selected: false, default: true, forced: false },
                { id: 2, type: "audio", title: "Commentary", language: "eng", codec: "opus", selected: true, default: false, forced: false },
                { id: 3, type: "subtitle", title: "English SDH", language: "eng", codec: "ass", selected: true, default: false, forced: false },
            ],
        });
        expect(audio.value).toBe("2");
        expect(subtitles.value).toBe("3");

        subtitles.value = "no";
        subtitles.dispatchEvent(new Event("change"));
        await vi.waitFor(() => expect(apiMocks.nativeMediaCommand).toHaveBeenCalledWith(MKV_SESSION_ID, ["set", "sid", "no"]));

        const injected = document.createElement("option");
        injected.value = "999";
        audio.appendChild(injected);
        audio.value = "999";
        audio.dispatchEvent(new Event("change"));
        expect(apiMocks.nativeMediaCommand).not.toHaveBeenCalledWith(MKV_SESSION_ID, ["set", "aid", "999"]);

        document.querySelector<HTMLButtonElement>("#video-skip-back")?.click();
        await vi.waitFor(() => expect(apiMocks.nativeMediaCommand).toHaveBeenCalledWith(
            MKV_SESSION_ID,
            ["seek", "-10", "relative"],
        ));
        expect(apiMocks.nativeMediaCommand).not.toHaveBeenCalledWith(MKV_SESSION_ID, ["seek", "-20", "relative"]);

        await videoModule.closeVideoModal();
    });

    it("hides single-audio controls and clears tracks across player switches", async () => {
        apiMocks.openNativeMedia.mockResolvedValue(nativeOpenResult(21, OLD_MKV_SESSION_ID));
        apiMocks.openMedia.mockResolvedValue(mediaOpenResult(22, "html-token"));

        const videoModule = await import("./video");
        videoModule.setupVideoModal();
        await videoModule.openVideoModal({ id: 21, name: "movie-21.mkv", size: 1024 });

        const staleStateCallback = runtimeMocks.events.get("native_media_state");
        staleStateCallback?.({
            token: OLD_MKV_SESSION_ID,
            tracks: [
                { id: 1, type: "audio", language: "eng", codec: "aac", selected: true, default: true, forced: false },
                { id: 4, type: "subtitle", language: "spa", codec: "srt", selected: false, default: false, forced: false },
            ],
        });

        expect(document.querySelector<HTMLElement>("#video-audio-control")?.hidden).toBe(true);
        expect(document.querySelector<HTMLElement>("#video-subtitle-control")?.hidden).toBe(false);

        await videoModule.openVideoModal({ id: 22, name: "clip-22.mp4", size: 1024 });

        const controls = document.querySelector<HTMLElement>("#video-track-controls");
        const audio = document.querySelector<HTMLSelectElement>("#video-audio-select");
        const subtitles = document.querySelector<HTMLSelectElement>("#video-subtitle-select");
        expect(controls?.hidden).toBe(true);
        expect(audio?.options).toHaveLength(0);
        expect(subtitles?.options).toHaveLength(0);

        staleStateCallback?.({
            token: OLD_MKV_SESSION_ID,
            tracks: [
                { id: 1, type: "audio", selected: true, default: true, forced: false },
                { id: 2, type: "audio", selected: false, default: false, forced: false },
                { id: 4, type: "subtitle", selected: true, default: false, forced: false },
            ],
        });
        expect(controls?.hidden).toBe(true);
        expect(audio?.options).toHaveLength(0);
        expect(subtitles?.options).toHaveLength(0);

        await videoModule.closeVideoModal();
    });
});

describe("encrypted media lifecycle", () => {
    it("installs one vault-lock listener and closes an active encrypted session", async () => {
        apiMocks.openNativeMedia.mockResolvedValue(nativeOpenResult(30, "encrypted-token"));

        const videoModule = await import("./video");
        videoModule.setupVideoModal();
        videoModule.setupVideoModal();

        expect(runtimeMocks.eventsOn.mock.calls.filter(([name]) => name === "encrypted_media_sessions_closed")).toHaveLength(1);

        await videoModule.openVideoModal({ id: 30, name: "encrypted.mkv", size: 1024, encrypted: true });
        runtimeMocks.events.get("encrypted_media_sessions_closed")?.({});

        await vi.waitFor(() => expect(apiMocks.closeNativeMedia).toHaveBeenCalledWith("encrypted-token"));
        expect(document.querySelector<HTMLElement>("#video-modal")?.style.display).toBe("none");
    });

    it("uses authoritative opened-session metadata when the caller omits encryption", async () => {
        const opened = nativeOpenResult(31, "authoritative-encrypted-token");
        opened.info.encrypted = true;
        apiMocks.openNativeMedia.mockResolvedValue(opened);

        const videoModule = await import("./video");
        videoModule.setupVideoModal();
        await videoModule.openVideoModal({ id: 31, name: "encrypted-from-backend.mkv", size: 1024 });
        runtimeMocks.events.get("encrypted_media_sessions_closed")?.({});

        await vi.waitFor(() => expect(apiMocks.closeNativeMedia).toHaveBeenCalledWith("authoritative-encrypted-token"));
        expect(document.querySelector<HTMLElement>("#video-modal")?.style.display).toBe("none");
    });
});

describe("native player failures", () => {
    it("replays a newer failure emitted before the open call resolves", async () => {
        const opened = nativeOpenResult(39, "early-failed-native-session");
        apiMocks.openNativeMedia.mockImplementation(async () => {
            const listener = runtimeMocks.events.get("native_media_state");
            expect(listener).toBeTypeOf("function");
            listener?.({
                token: opened.token,
                sequence: 2,
                status: "failed",
                error: "native media player exited unexpectedly",
                paused: true,
            });
            return opened;
        });

        const videoModule = await import("./video");
        videoModule.setupVideoModal();
        await videoModule.openVideoModal({ id: 39, name: "early-failed.mkv", size: 1024 });

        await vi.waitFor(() => expect(apiMocks.closeNativeMedia).toHaveBeenCalledWith(opened.token));
        expect(apiMocks.closeNativeMedia).toHaveBeenCalledOnce();
        expect(document.querySelector("#video-error")?.textContent).toContain("compatible player stopped unexpectedly");
        expect(document.querySelector<HTMLElement>("#video-loading")?.style.display).toBe("none");
        expect(document.querySelector<HTMLElement>("#video-modal")?.classList.contains("is-video-loading")).toBe(false);
    });

    it("surfaces an asynchronous native failure once and releases its session", async () => {
        apiMocks.openNativeMedia.mockResolvedValue(nativeOpenResult(40, FAILED_NATIVE_SESSION_ID));

        const videoModule = await import("./video");
        videoModule.setupVideoModal();
        await videoModule.openVideoModal({ id: 40, name: "failed.mkv", size: 1024 });

        const stateCallback = runtimeMocks.events.get("native_media_state");
        stateCallback?.({
            token: FAILED_NATIVE_SESSION_ID,
            status: "failed",
            error: "native media player exited unexpectedly",
            paused: true,
        });
        stateCallback?.({
            token: FAILED_NATIVE_SESSION_ID,
            status: "failed",
            error: "duplicate failure",
            paused: true,
        });

        await vi.waitFor(() => expect(apiMocks.closeNativeMedia).toHaveBeenCalledWith(FAILED_NATIVE_SESSION_ID));
        expect(apiMocks.closeNativeMedia).toHaveBeenCalledOnce();
        expect(document.querySelector<HTMLElement>("#video-modal")?.style.display).toBe("flex");
        expect(document.querySelector("#video-error")?.textContent).toContain("compatible player stopped unexpectedly");
    });

    it("shows honest standalone playback UX and treats a normal window close as user intent", async () => {
        const opened = nativeOpenResult(41, "wayland-standalone-session");
        opened.presentation = "standalone";
        opened.initialState = {
            token: opened.token,
            sequence: 1,
            status: "playing",
            paused: false,
            loading: false,
            volume: 1,
            rate: 1,
        };
        apiMocks.openNativeMedia.mockResolvedValue(opened);

        const videoModule = await import("./video");
        videoModule.setupVideoModal();
        await videoModule.openVideoModal({ id: 41, name: "wayland.mkv", size: 1024 });
        await nextTasks();

        const modal = document.querySelector<HTMLElement>("#video-modal");
        const standalone = document.querySelector<HTMLElement>("#video-standalone");
        expect(modal?.classList.contains("is-video-native-standalone")).toBe(true);
        expect(standalone?.hidden).toBe(false);
        expect(standalone?.textContent).toContain("separate window");
        expect(apiMocks.resizeNativeMedia).not.toHaveBeenCalled();

        runtimeMocks.events.get("native_media_state")?.({
            token: opened.token,
            sequence: 2,
            status: "closed",
            paused: true,
        });

        await vi.waitFor(() => expect(apiMocks.closeNativeMedia).toHaveBeenCalledWith(opened.token));
        expect(document.querySelector<HTMLElement>("#video-modal")?.style.display).toBe("none");
        expect(document.querySelector("#video-error")?.textContent ?? "").not.toContain("stopped unexpectedly");
    });
});
