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

function openSettings(section: "picture" | "audio" | "subtitle" | "speed") {
    if (document.querySelector<HTMLElement>("#video-settings-panel")?.hidden) {
        document.querySelector<HTMLButtonElement>("#video-picture-button")?.click();
    }
    document.querySelector<HTMLButtonElement>(`[data-settings-section="${section}"]`)?.click();
}

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

        expect(apiMocks.nativeMediaCommand).toHaveBeenCalledWith(SHARED_SESSION_ID, ["set", "pause", "yes"]);
        expect(apiMocks.nativeMediaCommand).not.toHaveBeenCalledWith(SHARED_SESSION_ID, ["cycle", "pause"]);
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

describe("macOS native video layering", () => {
    it("makes the document canvas transparent only while HTML controls overlay native video", async () => {
        const opened = nativeOpenResult(19, "macos-overlay-session");
        opened.htmlControls = true;
        apiMocks.openNativeMedia.mockResolvedValue(opened);

        const videoModule = await import("./video");
        videoModule.setupVideoModal();
        await videoModule.openVideoModal({ id: 19, name: "movie-19.mkv", size: 1024 });

        expect(document.documentElement.classList.contains("native-video-active")).toBe(true);
        expect(document.body.classList.contains("native-video-active")).toBe(true);

        await videoModule.closeVideoModal();

        expect(document.documentElement.classList.contains("native-video-active")).toBe(false);
        expect(document.body.classList.contains("native-video-active")).toBe(false);
    });
});

describe("native seek preview platform capability", () => {
    it("marks Windows fallback playback as native-overlay capable", async () => {
        vi.spyOn(window.navigator, "userAgent", "get")
            .mockReturnValue("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36");
        apiMocks.openNativeMedia.mockResolvedValue(nativeOpenResult(23, "windows-overlay-session"));

        const videoModule = await import("./video");
        videoModule.setupVideoModal();
        await videoModule.openVideoModal({ id: 23, name: "windows.mkv", size: 1024 });

        expect(document.querySelector("#video-modal")?.classList.contains("has-native-seek-overlay")).toBe(true);
        await videoModule.closeVideoModal();
    });

    it("keeps Linux fallback on its in-controls timestamp preview", async () => {
        vi.spyOn(window.navigator, "userAgent", "get")
            .mockReturnValue("Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36");
        apiMocks.openNativeMedia.mockResolvedValue(nativeOpenResult(24, "linux-fallback-session"));

        const videoModule = await import("./video");
        videoModule.setupVideoModal();
        await videoModule.openVideoModal({ id: 24, name: "linux.mkv", size: 1024 });

        expect(document.querySelector("#video-modal")?.classList.contains("has-native-seek-overlay")).toBe(false);
        await videoModule.closeVideoModal();
    });
});

describe("native video track pickers", () => {
    const tracks = [
        { id: 1, type: "audio", title: "Main", language: "eng", codec: "aac", selected: true, default: true, forced: false },
        { id: 2, type: "audio", title: "Commentary", language: "eng", codec: "opus", selected: false, default: false, forced: false },
        { id: 3, type: "subtitle", title: "English SDH", language: "eng", codec: "ass", selected: false, default: false, forced: false },
    ];
    const menuLabels = (menu: HTMLElement | null) => Array.from(menu?.querySelectorAll("[data-track]") ?? [])
        .map((item) => item.textContent?.replace("✓", "").trim());
    const commandCount = (command: string[]) => apiMocks.nativeMediaCommand.mock.calls
        .filter(([, sent]) => JSON.stringify(sent) === JSON.stringify(command)).length;

    it("offers audio and subtitle menus and sends validated mpv commands", async () => {
        const opened = nativeOpenResult(20, MKV_SESSION_ID);
        opened.htmlControls = true;
        apiMocks.openNativeMedia.mockResolvedValue(opened);

        const videoModule = await import("./video");
        videoModule.setupVideoModal();
        videoModule.setupVideoModal();
        await videoModule.openVideoModal({ id: 20, name: "movie-20.mkv", size: 1024 });

        runtimeMocks.events.get("native_media_state")?.({ token: MKV_SESSION_ID, paused: false, duration: 240, tracks });
        await nextTasks();

        const audioButton = document.querySelector<HTMLButtonElement>("#video-audio-button");
        const subtitleButton = document.querySelector<HTMLButtonElement>("#video-subtitle-button");
        const audioMenu = document.querySelector<HTMLElement>("#video-audio-menu");
        const subtitleMenu = document.querySelector<HTMLElement>("#video-subtitle-menu");

        expect(document.querySelector<HTMLElement>("#video-audio-wrap")?.hidden).toBe(false);
        expect(document.querySelector<HTMLElement>("#video-subtitle-wrap")?.hidden).toBe(false);
        expect(document.querySelector("#video-audio-label")?.textContent).toBe("ENG");
        expect(document.querySelector("#video-subtitle-label")?.textContent).toBe("Off");
        expect(subtitleButton?.dataset.state).toBe("off");
        expect(menuLabels(audioMenu)).toEqual(["Main / ENG / AAC", "Commentary / ENG / OPUS"]);
        expect(menuLabels(subtitleMenu)).toEqual(["Off", "English SDH / ENG / ASS"]);
        expect(audioMenu?.querySelector('[data-track="1"]')?.getAttribute("aria-checked")).toBe("true");

        openSettings("audio");
        expect(audioMenu?.classList.contains("is-open")).toBe(true);
        expect(audioButton?.hasAttribute("aria-expanded")).toBe(false);
        openSettings("subtitle");
        expect(audioMenu?.classList.contains("is-open")).toBe(false);
        expect(subtitleMenu?.classList.contains("is-open")).toBe(true);
        subtitleMenu?.querySelector<HTMLButtonElement>('[data-track="3"]')?.click();
        expect(subtitleMenu?.classList.contains("is-open")).toBe(false);
        openSettings("audio");
        audioMenu?.querySelector<HTMLButtonElement>('[data-track="2"]')?.click();

        await vi.waitFor(() => {
            expect(apiMocks.nativeMediaCommand).toHaveBeenCalledWith(MKV_SESSION_ID, ["set", "aid", "2"]);
            expect(apiMocks.nativeMediaCommand).toHaveBeenCalledWith(MKV_SESSION_ID, ["set", "sid", "3"]);
        });
        expect(commandCount(["set", "aid", "2"])).toBe(1);
        expect(document.querySelector("#video-audio-label")?.textContent).toBe("ENG");
        expect(document.querySelector("#video-subtitle-label")?.textContent).toBe("ENG");
        expect(subtitleButton?.dataset.state).toBe("on");

        runtimeMocks.events.get("native_media_state")?.({
            token: MKV_SESSION_ID,
            tracks: tracks.map((track) => ({ ...track, selected: track.id !== 1 })),
        });
        expect(audioMenu?.querySelector('[data-track="2"]')?.classList.contains("is-selected")).toBe(true);
        expect(subtitleMenu?.querySelector('[data-track="3"]')?.getAttribute("aria-checked")).toBe("true");

        openSettings("subtitle");
        subtitleMenu?.querySelector<HTMLButtonElement>('[data-track="no"]')?.click();
        await vi.waitFor(() => expect(apiMocks.nativeMediaCommand).toHaveBeenCalledWith(MKV_SESSION_ID, ["set", "sid", "no"]));
        expect(document.querySelector("#video-subtitle-label")?.textContent).toBe("Off");

        document.body.dispatchEvent(new KeyboardEvent("keydown", { key: "c", bubbles: true }));
        await vi.waitFor(() => expect(commandCount(["set", "sid", "3"])).toBe(2));

        await videoModule.closeVideoModal();
    });

    it("cycles native tracks by actual IDs, wraps through Off, and never opens settings", async () => {
        apiMocks.openNativeMedia.mockResolvedValue(nativeOpenResult(23, MKV_SESSION_ID));
        const videoModule = await import("./video");
        videoModule.setupVideoModal();
        await videoModule.openVideoModal({ id: 23, name: "movie.mkv" });
        runtimeMocks.events.get("native_media_state")?.({ token: MKV_SESSION_ID, tracks: [
            { ...tracks[0], id: 7 }, { ...tracks[1], id: 42 },
            { ...tracks[2], id: 19 }, { ...tracks[2], id: 83, title: "French", language: "fra" },
        ] });
        await nextTasks();
        const audio = document.querySelector<HTMLButtonElement>("#video-audio-button")!;
        const subtitle = document.querySelector<HTMLButtonElement>("#video-subtitle-button")!;
        apiMocks.nativeMediaCommand.mockClear();
        audio.click();
        document.querySelector<HTMLButtonElement>("#video-speed-button")?.click();
        audio.click();
        subtitle.click(); subtitle.click(); subtitle.click();
        expect(apiMocks.nativeMediaCommand.mock.calls.map(([, command]) => command).filter((command) => ["aid", "sid"].includes(command[1]))).toEqual([
            ["set", "aid", "42"], ["set", "aid", "7"],
            ["set", "sid", "19"], ["set", "sid", "83"], ["set", "sid", "no"],
        ]);
        expect(subtitle.title).toContain("Off");
        expect(audio.title).toContain("Main");
        expect(document.querySelector<HTMLElement>("#video-settings-panel")?.hidden).toBe(true);
        expect(document.querySelector(".video-menu.is-open")).toBeNull();
        expect(document.querySelector('input[type="search"]')).toBeNull();
        runtimeMocks.events.get("native_media_state")?.({ token: MKV_SESSION_ID, tracks: [{ ...tracks[0], id: 99 }] });
        audio.click();
        expect(apiMocks.nativeMediaCommand).toHaveBeenLastCalledWith(MKV_SESSION_ID, ["set", "aid", "99"]);
        document.querySelector<HTMLButtonElement>("#video-aspect-button")?.click();
        await vi.waitFor(() => expect(apiMocks.nativeMediaCommand).toHaveBeenCalledWith(MKV_SESSION_ID, ["set", "panscan", "1"]));
        expect(document.querySelector<HTMLElement>("#video-settings-panel")?.hidden).toBe(true);
        await videoModule.closeVideoModal();
    });

    it("applies picture and subtitle appearance settings through validated native commands", async () => {
        const storage = new Map<string, string>();
        vi.stubGlobal("localStorage", {
            getItem: (key: string) => storage.get(key) ?? null,
            setItem: (key: string, value: string) => storage.set(key, value),
        });
        apiMocks.openNativeMedia.mockResolvedValue(nativeOpenResult(26, MKV_SESSION_ID));

        const videoModule = await import("./video");
        videoModule.setupVideoModal();
        await videoModule.openVideoModal({ id: 26, name: "movie-26.mkv", size: 1024 });
        await nextTasks();
        apiMocks.nativeMediaCommand.mockClear();

        document.querySelector<HTMLButtonElement>("#video-picture-button")?.click();
        document.querySelector<HTMLButtonElement>('[data-picture-mode="fill"]')?.click();
        await nextTasks();

        expect(apiMocks.nativeMediaCommand).toHaveBeenCalledWith(MKV_SESSION_ID, ["set", "panscan", "1"]);
        expect(apiMocks.nativeMediaCommand).toHaveBeenCalledWith(MKV_SESSION_ID, ["set", "video-unscaled", "no"]);
        expect(apiMocks.nativeMediaCommand).toHaveBeenCalledWith(MKV_SESSION_ID, ["set", "video-aspect-override", "no"]);

        document.querySelector<HTMLButtonElement>('[data-settings-section="subtitle"]')?.click();
        const size = document.querySelector<HTMLInputElement>("#video-subtitle-size");
        const color = document.querySelector<HTMLInputElement>("#video-subtitle-color");
        const background = document.querySelector<HTMLInputElement>("#video-subtitle-background");
        const save = document.querySelector<HTMLButtonElement>("#video-subtitle-save");
        expect(size && color && background && save).toBeTruthy();
        if (!size || !color || !background || !save) return;

        size.value = "52";
        size.dispatchEvent(new Event("input", { bubbles: true }));
        color.value = "#ffcc00";
        color.dispatchEvent(new Event("input", { bubbles: true }));
        background.checked = true;
        background.dispatchEvent(new Event("change", { bubbles: true }));
        save.click();
        await nextTasks();

        expect(apiMocks.nativeMediaCommand).toHaveBeenCalledWith(MKV_SESSION_ID, ["set", "sub-font-size", "52"]);
        expect(apiMocks.nativeMediaCommand).toHaveBeenCalledWith(MKV_SESSION_ID, ["set", "sub-color", "#FFCC00"]);
        expect(apiMocks.nativeMediaCommand).toHaveBeenCalledWith(MKV_SESSION_ID, ["set", "sub-back-color", "#AF000000"]);
        expect(apiMocks.nativeMediaCommand).toHaveBeenCalledWith(MKV_SESSION_ID, ["set", "sub-ass-override", "strip"]);
        expect([...storage.values()].some((value) => value.includes('"pictureMode":"fill"'))).toBe(true);

        await videoModule.closeVideoModal();
    });

    it("shows the single-audio picker and clears tracks across player switches", async () => {
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

        expect(document.querySelector<HTMLElement>("#video-audio-wrap")?.hidden).toBe(false);
        openSettings("audio");
        expect(document.querySelector('[data-track="1"]')?.getAttribute("aria-checked")).toBe("true");
        expect(document.querySelector<HTMLElement>("#video-subtitle-wrap")?.hidden).toBe(false);

        await videoModule.openVideoModal({ id: 22, name: "clip-22.mp4", size: 1024 });

        const audioMenu = document.querySelector<HTMLElement>("#video-audio-menu");
        const subtitleMenu = document.querySelector<HTMLElement>("#video-subtitle-menu");
        expect(document.querySelector<HTMLElement>("#video-audio-wrap")?.hidden).toBe(true);
        expect(document.querySelector<HTMLElement>("#video-subtitle-wrap")?.hidden).toBe(true);
        expect(menuLabels(subtitleMenu)).toHaveLength(0);

        staleStateCallback?.({
            token: OLD_MKV_SESSION_ID,
            tracks: [
                { id: 1, type: "audio", selected: true, default: true, forced: false },
                { id: 2, type: "audio", selected: false, default: false, forced: false },
                { id: 4, type: "subtitle", selected: true, default: false, forced: false },
            ],
        });
        expect(document.querySelector<HTMLElement>("#video-audio-wrap")?.hidden).toBe(true);
        expect(menuLabels(audioMenu)).toHaveLength(0);
        expect(menuLabels(subtitleMenu)).toHaveLength(0);

        await videoModule.closeVideoModal();
    });

    it("closes the settings dock and releases its native viewport reservation", async () => {
        apiMocks.openNativeMedia.mockResolvedValue(nativeOpenResult(27, MKV_SESSION_ID));
        vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function (this: HTMLElement) {
            if (this.id === "video-stage") {
                return { x: 0, y: 0, top: 0, right: 800, bottom: 450, left: 0, width: 800, height: 450, toJSON: () => ({}) };
            }
            if (this.id === "video-settings-panel") {
                return { x: 500, y: 60, top: 60, right: 790, bottom: 350, left: 500, width: 290, height: 290, toJSON: () => ({}) };
            }
            if (this.classList.contains("video-topbar")) {
                return { x: 0, y: 0, top: 0, right: 800, bottom: 58, left: 0, width: 800, height: 58, toJSON: () => ({}) };
            }
            if (this.classList.contains("video-controls")) {
                return { x: 0, y: 350, top: 350, right: 800, bottom: 450, left: 0, width: 800, height: 100, toJSON: () => ({}) };
            }
            return { x: 0, y: 0, top: 0, right: 800, bottom: 450, left: 0, width: 800, height: 450, toJSON: () => ({}) };
        });

        const videoModule = await import("./video");
        videoModule.setupVideoModal();
        await videoModule.openVideoModal({ id: 27, name: "movie-27.mkv", size: 1024 });
        runtimeMocks.events.get("native_media_state")?.({ token: MKV_SESSION_ID, tracks });
        await nextTasks();

        openSettings("subtitle");
        await nextTasks();

        expect(document.querySelector<HTMLElement>("#video-settings-panel")?.hidden).toBe(false);
        expect(document.querySelector<HTMLElement>("#video-native-viewport")?.style.getPropertyValue("--video-native-right-inset")).toBe("304px");

        await videoModule.closeVideoModal();

        expect(document.querySelector<HTMLElement>("#video-settings-panel")?.hidden).toBe(true);
        expect(document.querySelector<HTMLElement>("#video-modal")?.classList.contains("has-video-settings")).toBe(false);
    });
});

describe("video picture settings", () => {
    it("applies fixed-ratio sizing to HTML playback", async () => {
        apiMocks.openMedia.mockResolvedValue(mediaOpenResult(28, "html-aspect-token"));

        const videoModule = await import("./video");
        videoModule.setupVideoModal();
        await videoModule.openVideoModal({ id: 28, name: "clip-28.mp4", size: 1024 });

        const video = document.querySelector<HTMLVideoElement>("#video-player");
        expect(video).toBeTruthy();
        if (!video) return;

        document.querySelector<HTMLButtonElement>("#video-picture-button")?.click();
        document.querySelector<HTMLButtonElement>('[data-picture-mode="4:3"]')?.click();
        await nextTasks();

        expect(video.style.width).toBe("600px");
        expect(video.style.height).toBe("450px");
        expect(video.style.objectFit).toBe("fill");

        await videoModule.closeVideoModal();
    });

    it("keeps playback shortcuts out of settings fields", async () => {
        const tracks = [
            { id: 1, type: "audio", title: "Main", language: "eng", codec: "aac", selected: true, default: true, forced: false },
            { id: 3, type: "subtitle", title: "English", language: "eng", codec: "srt", selected: false, default: true, forced: false },
        ];
        apiMocks.openNativeMedia.mockResolvedValue(nativeOpenResult(29, MKV_SESSION_ID));

        const videoModule = await import("./video");
        videoModule.setupVideoModal();
        await videoModule.openVideoModal({ id: 29, name: "movie-29.mkv", size: 1024 });
        runtimeMocks.events.get("native_media_state")?.({ token: MKV_SESSION_ID, tracks });
        await nextTasks();
        apiMocks.nativeMediaCommand.mockClear();

        openSettings("subtitle");
        const input = document.querySelector<HTMLInputElement>("#video-subtitle-size")!;
        input.focus();
        input.dispatchEvent(new KeyboardEvent("keydown", { key: "c", bubbles: true }));

        expect(apiMocks.nativeMediaCommand).not.toHaveBeenCalledWith(MKV_SESSION_ID, ["set", "sid", "3"]);

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

describe("playback settings dock", () => {
    it("shows every track without search and returns focus to the gear on Escape", async () => {
        apiMocks.openNativeMedia.mockResolvedValue(nativeOpenResult(31, MKV_SESSION_ID));
        const videoModule = await import("./video");
        videoModule.setupVideoModal();
        await videoModule.openVideoModal({ id: 31, name: "movie.mkv" });
        runtimeMocks.events.get("native_media_state")?.({ token: MKV_SESSION_ID, tracks: Array.from({ length: 50 }, (_, index) => ({ id: index + 1, type: "subtitle", title: `Language ${index + 1}`, selected: index === 29 })) });
        await nextTasks();
        openSettings("subtitle");
        const menu = document.querySelector<HTMLElement>("#video-subtitle-menu")!;
        expect(menu.querySelector("input")).toBeNull();
        expect(menu.querySelectorAll("[data-track]")).toHaveLength(51);
        const selected = menu.querySelector<HTMLButtonElement>('[data-track="30"]')!;
        expect(selected.getAttribute("aria-checked")).toBe("true");
        selected.focus();
        selected.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
        expect(document.querySelector<HTMLElement>("#video-settings-panel")?.hidden).toBe(true);
        expect(document.activeElement?.id).toBe("video-picture-button");
        openSettings("audio");
        expect(document.querySelector("#video-picture-button")?.getAttribute("aria-expanded")).toBe("true");
        document.querySelector<HTMLButtonElement>("#video-picture-button")?.click();
        expect(document.querySelector<HTMLElement>("#video-settings-panel")?.hidden).toBe(true);
        await videoModule.closeVideoModal();
    });

    it("cycles speed presets from custom values and synchronizes aspect cycling with picture settings", async () => {
        const saved = new Map<string, string>();
        vi.stubGlobal("localStorage", { getItem: (key: string) => saved.get(key) ?? null, setItem: (key: string, value: string) => saved.set(key, value) });
        apiMocks.openMedia.mockResolvedValue(mediaOpenResult(34, SHARED_SESSION_ID));
        const videoModule = await import("./video");
        videoModule.setupVideoModal();
        await videoModule.openVideoModal({ id: 34, name: "clip.mp4" });
        const video = document.querySelector<HTMLVideoElement>("#video-player")!;
        const speed = document.querySelector<HTMLButtonElement>("#video-speed-button")!;
        for (const rate of [1.25, 1.5, 2, .5, .75, 1]) {
            speed.click();
            expect(video.playbackRate).toBe(rate);
            expect(speed.getAttribute("aria-label")).toContain(`${rate}x`);
        }
        video.playbackRate = 1.65;
        video.dispatchEvent(new Event("ratechange"));
        speed.click();
        expect(video.playbackRate).toBe(2);
        video.playbackRate = 3;
        video.dispatchEvent(new Event("ratechange"));
        speed.click();
        expect(video.playbackRate).toBe(.5);
        const aspect = document.querySelector<HTMLButtonElement>("#video-aspect-button")!;
        for (const [mode, label] of [["fill", "Fill"], ["original", "Original"], ["16:9", "16:9"], ["4:3", "4:3"], ["fit", "Fit"]]) {
            aspect.click();
            await nextTasks();
            flushSync();
            expect(aspect.textContent).toContain(label);
            expect(aspect.title).toContain(label);
            expect(document.querySelector(`[data-picture-mode="${mode}"]`)?.getAttribute("aria-pressed")).toBe("true");
            expect(Array.from(saved.values())).toContainEqual(expect.stringContaining(`"pictureMode":"${mode}"`));
            expect(document.querySelector<HTMLElement>("#video-settings-panel")?.hidden).toBe(true);
        }
        openSettings("picture");
        document.querySelector<HTMLButtonElement>('[data-picture-mode="fill"]')?.click();
        flushSync();
        expect(aspect.textContent).toContain("Fill");
        expect(video.style.objectFit).toBe("cover");
        await videoModule.closeVideoModal();
    });

    it.each([
        ["size", "60", "sub-font-size", "60"],
        ["color", "#ffff00", "sub-color", "#FFFF00"],
        ["outline", "4", "sub-outline-size", "4"],
        ["background", "", "sub-border-style", "background-box"],
    ])("previews %s locally and saves subtitle overrides explicitly", async (field, value, property, expected) => {
        const setItem = vi.fn();
        vi.stubGlobal("localStorage", { getItem: () => null, setItem });
        apiMocks.openNativeMedia.mockResolvedValue(nativeOpenResult(32, MKV_SESSION_ID));
        const videoModule = await import("./video");
        videoModule.setupVideoModal();
        await videoModule.openVideoModal({ id: 32, name: "styled-subtitles.mkv" });
        await vi.waitFor(() => expect(apiMocks.nativeMediaCommand).toHaveBeenCalledWith(MKV_SESSION_ID, ["set", "sub-ass-override", "scale"]));
        openSettings("subtitle");
        apiMocks.nativeMediaCommand.mockClear();
        const control = document.querySelector<HTMLInputElement>(`#video-subtitle-${field}`)!;
        if (field === "background") control.click();
        else {
            control.value = value;
            control.dispatchEvent(new Event("input", { bubbles: true }));
        }
        flushSync();
        await nextTasks();
        expect(apiMocks.nativeMediaCommand).not.toHaveBeenCalled();
        expect(setItem).not.toHaveBeenCalled();
        expect(document.querySelector("#video-subtitle-override")).toBeNull();
        document.querySelector<HTMLButtonElement>("#video-subtitle-save")!.click();
        flushSync();
        await vi.waitFor(() => {
            expect(apiMocks.nativeMediaCommand).toHaveBeenCalledWith(MKV_SESSION_ID, ["set", property, expected]);
            expect(apiMocks.nativeMediaCommand).toHaveBeenCalledWith(MKV_SESSION_ID, ["set", "sub-ass-override", "strip"]);
        });
        expect(setItem).toHaveBeenCalledWith("tdrive.playback-appearance.v1", expect.stringContaining('"overrideStyledSubtitles":true'));
        apiMocks.nativeMediaCommand.mockClear();
        document.querySelector<HTMLButtonElement>("#video-subtitle-reset")!.click();
        flushSync();
        await vi.waitFor(() => expect(apiMocks.nativeMediaCommand).toHaveBeenCalledWith(MKV_SESSION_ID, ["set", "sub-ass-override", "scale"]));
        expect(setItem).toHaveBeenLastCalledWith("tdrive.playback-appearance.v1", expect.stringContaining('"overrideStyledSubtitles":false'));
        await videoModule.closeVideoModal();
    });

    it("saves unchanged legacy custom appearance and keeps drafts separate from picture changes", async () => {
        const legacy = { pictureMode: "fit", subtitleFontSize: 52, subtitleColor: "#FF0000", subtitleOutlineSize: 3, subtitleBackground: true, overrideStyledSubtitles: false };
        const setItem = vi.fn();
        vi.stubGlobal("localStorage", { getItem: () => JSON.stringify(legacy), setItem });
        apiMocks.openNativeMedia.mockResolvedValue(nativeOpenResult(32, MKV_SESSION_ID));
        const videoModule = await import("./video");
        videoModule.setupVideoModal();
        await videoModule.openVideoModal({ id: 32, name: "legacy.mkv" });
        await nextTasks();
        openSettings("subtitle");
        document.querySelector<HTMLButtonElement>("#video-subtitle-save")!.click();
        flushSync();
        await vi.waitFor(() => expect(apiMocks.nativeMediaCommand).toHaveBeenCalledWith(MKV_SESSION_ID, ["set", "sub-ass-override", "strip"]));
        expect(JSON.parse(setItem.mock.lastCall![1])).toEqual({ ...legacy, overrideStyledSubtitles: true });
        const color = document.querySelector<HTMLInputElement>("#video-subtitle-color")!;
        color.value = "#00ff00";
        color.dispatchEvent(new Event("input", { bubbles: true }));
        await nextTasks();
        document.querySelector<HTMLButtonElement>("#video-aspect-button")!.click();
        await nextTasks();
        expect(color.value.toLowerCase()).toBe("#00ff00");
        expect(JSON.parse(setItem.mock.lastCall![1])).toEqual({ ...legacy, pictureMode: "fill", overrideStyledSubtitles: true });
        openSettings("picture");
        document.querySelector<HTMLButtonElement>('[data-picture-mode="4:3"]')!.click();
        await nextTasks();
        expect(color.value.toLowerCase()).toBe("#00ff00");
        expect(JSON.parse(setItem.mock.lastCall![1])).toEqual({ ...legacy, pictureMode: "4:3", overrideStyledSubtitles: true });
        openSettings("subtitle");
        document.querySelector<HTMLButtonElement>("#video-subtitle-save")!.click();
        flushSync();
        expect(JSON.parse(setItem.mock.lastCall![1])).toEqual({ ...legacy, pictureMode: "4:3", subtitleColor: "#00FF00", overrideStyledSubtitles: true });
        await videoModule.closeVideoModal();
    });

    it("explains when the selected subtitle track cannot use text styling", async () => {
        apiMocks.openNativeMedia.mockResolvedValue(nativeOpenResult(32, MKV_SESSION_ID));
        const videoModule = await import("./video");
        videoModule.setupVideoModal();
        await videoModule.openVideoModal({ id: 32, name: "image-subtitles.mkv" });
        runtimeMocks.events.get("native_media_state")?.({ token: MKV_SESSION_ID, tracks: [
            { id: 1, type: "subtitle", codec: "hdmv_pgs_subtitle", selected: true },
            { id: 2, type: "subtitle", codec: "subrip", selected: false },
        ] });
        await nextTasks();
        openSettings("subtitle");
        const notice = document.querySelector<HTMLElement>("#video-subtitle-format-note")!;
        expect(notice.hidden).toBe(false);
        expect(notice.textContent).toContain("image-based");
        document.querySelector<HTMLButtonElement>("#video-subtitle-button")?.click();
        expect(notice.hidden).toBe(true);
        await videoModule.closeVideoModal();
    });

    it("applies picture and subtitle preferences, resets appearance, and uses an exact native speed", async () => {
        apiMocks.openNativeMedia.mockResolvedValue(nativeOpenResult(32, MKV_SESSION_ID));
        const videoModule = await import("./video");
        videoModule.setupVideoModal();
        await videoModule.openVideoModal({ id: 32, name: "movie.mkv", size: 1024 });
        await nextTasks();
        document.querySelector<HTMLButtonElement>("#video-picture-button")?.click();
        document.querySelector<HTMLButtonElement>('[data-picture-mode="4:3"]')?.click();
        flushSync();
        await vi.waitFor(() => expect(apiMocks.nativeMediaCommand).toHaveBeenCalledWith(MKV_SESSION_ID, ["set", "video-aspect-override", "4:3"]));
        document.querySelector<HTMLButtonElement>('[data-settings-section="subtitle"]')?.click();
        expect(document.querySelector<HTMLElement>("#video-subtitle-settings")?.hidden).toBe(false);
        const size = document.querySelector<HTMLInputElement>("#video-subtitle-size")!;
        size.value = "60";
        size.dispatchEvent(new Event("input", { bubbles: true }));
        const color = document.querySelector<HTMLInputElement>("#video-subtitle-color")!;
        color.value = "#ffff00";
        color.dispatchEvent(new Event("input", { bubbles: true }));
        document.querySelector<HTMLInputElement>("#video-subtitle-background")?.click();
        flushSync();
        document.querySelector<HTMLButtonElement>("#video-subtitle-save")!.click();
        flushSync();
        await vi.waitFor(() => {
            expect(apiMocks.nativeMediaCommand).toHaveBeenCalledWith(MKV_SESSION_ID, ["set", "sub-font-size", "60"]);
            expect(apiMocks.nativeMediaCommand).toHaveBeenCalledWith(MKV_SESSION_ID, ["set", "sub-color", "#FFFF00"]);
            expect(apiMocks.nativeMediaCommand).toHaveBeenCalledWith(MKV_SESSION_ID, ["set", "sub-border-style", "background-box"]);
            expect(apiMocks.nativeMediaCommand).toHaveBeenCalledWith(MKV_SESSION_ID, ["set", "sub-ass-override", "strip"]);
        });
        document.querySelector<HTMLButtonElement>("#video-subtitle-reset")?.click();
        flushSync();
        await vi.waitFor(() => expect(apiMocks.nativeMediaCommand).toHaveBeenCalledWith(MKV_SESSION_ID, ["set", "sub-font-size", "38"]));
        expect(document.querySelector<HTMLButtonElement>('[data-picture-mode="4:3"]')?.getAttribute("aria-pressed")).toBe("true");
        openSettings("speed");
        expect(document.querySelector("#video-speed-menu")?.classList.contains("is-open")).toBe(true);
        document.querySelector<HTMLButtonElement>('[data-rate="1.5"]')?.click();
        await vi.waitFor(() => expect(apiMocks.nativeMediaCommand).toHaveBeenCalledWith(MKV_SESSION_ID, ["set", "speed", "1.5"]));
        await videoModule.closeVideoModal();
        expect(document.querySelector<HTMLElement>("#video-settings-panel")?.hidden).toBe(true);
    });

    it("applies HTML picture sizing and carries saved preferences into the next native session", async () => {
        const saved = new Map<string, string>();
        vi.stubGlobal("localStorage", { getItem: (key: string) => saved.get(key) ?? null, setItem: (key: string, value: string) => saved.set(key, value) });
        apiMocks.openMedia.mockResolvedValue(mediaOpenResult(34, SHARED_SESSION_ID));
        apiMocks.openNativeMedia.mockResolvedValue(nativeOpenResult(35, MKV_SESSION_ID));
        const videoModule = await import("./video");
        videoModule.setupVideoModal();
        await videoModule.openVideoModal({ id: 34, name: "clip.mp4", size: 1024 });
        document.querySelector<HTMLButtonElement>("#video-picture-button")?.click();
        document.querySelector<HTMLButtonElement>('[data-picture-mode="fill"]')?.click();
        flushSync();
        expect(document.querySelector<HTMLVideoElement>("#video-player")?.style.objectFit).toBe("cover");
        expect(Array.from(saved.values())).toContainEqual(expect.stringContaining('"pictureMode":"fill"'));
        await videoModule.openVideoModal({ id: 35, name: "movie.mkv", size: 1024 });
        expect(document.querySelector<HTMLElement>("#video-settings-panel")?.hidden).toBe(true);
        await vi.waitFor(() => expect(apiMocks.nativeMediaCommand).toHaveBeenCalledWith(MKV_SESSION_ID, ["set", "panscan", "1"]));
        await videoModule.closeVideoModal();
    });

    it("reserves and restores native video space beside the dock and below compact controls", async () => {
        let compact = false;
        vi.spyOn(window, "matchMedia").mockImplementation((query) => ({ matches: compact && query.includes("760"), media: query, onchange: null, addListener: vi.fn(), removeListener: vi.fn(), addEventListener: vi.fn(), removeEventListener: vi.fn(), dispatchEvent: vi.fn() }));
        vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function (this: HTMLElement) {
            let left = 0, top = 0, width = 1000, height = 700;
            if (this.classList.contains("video-topbar")) height = 70;
            if (this.classList.contains("video-controls")) { top = 600; height = 100; }
            if (this.id === "video-settings-panel") { left = compact ? 10 : 658; top = compact ? 350 : 70; width = compact ? 980 : 330; height = compact ? 250 : 530; }
            if (this.id === "video-native-viewport") {
                left = Number.parseFloat(this.style.getPropertyValue("--video-native-side-inset")) || 0;
                top = Number.parseFloat(this.style.getPropertyValue("--video-native-top-inset")) || 0;
                width = 1000 - left - (Number.parseFloat(this.style.getPropertyValue("--video-native-right-inset")) || 0);
                height = 700 - top - (Number.parseFloat(this.style.getPropertyValue("--video-native-bottom-inset")) || 0);
            }
            return { x: left, y: top, top, left, right: left + width, bottom: top + height, width, height, toJSON: () => ({}) };
        });
        apiMocks.openNativeMedia.mockResolvedValue(nativeOpenResult(33, MKV_SESSION_ID));
        const videoModule = await import("./video");
        videoModule.setupVideoModal();
        await videoModule.openVideoModal({ id: 33, name: "movie.mkv", size: 1024 });
        await nextTasks();
        const viewport = document.querySelector<HTMLElement>("#video-native-viewport")!;
        const original = viewport.getBoundingClientRect();
        document.querySelector<HTMLButtonElement>("#video-picture-button")?.click();
        await nextTasks();
        expect(viewport.getBoundingClientRect().right).toBeLessThan(658);
        await vi.waitFor(() => expect(apiMocks.resizeNativeMedia).toHaveBeenCalledWith(MKV_SESSION_ID, expect.objectContaining({ width: viewport.getBoundingClientRect().width })));
        compact = true;
        window.dispatchEvent(new Event("resize"));
        await nextTasks();
        expect(viewport.getBoundingClientRect().bottom).toBeLessThan(350);
        expect(viewport.getBoundingClientRect().height).toBeGreaterThan(0);
        document.querySelector<HTMLButtonElement>("#video-settings-close")?.click();
        compact = false;
        window.dispatchEvent(new Event("resize"));
        await nextTasks();
        expect(viewport.getBoundingClientRect().width).toBe(original.width);
        expect(viewport.getBoundingClientRect().height).toBe(original.height);
        await videoModule.closeVideoModal();
    });
});


describe("playback speed slider", () => {
    it("changes native speed without closing the panel or hijacking arrow keys", async () => {
        apiMocks.openNativeMedia.mockResolvedValue(nativeOpenResult(40, MKV_SESSION_ID));
        const videoModule = await import("./video");
        videoModule.setupVideoModal();
        await videoModule.openVideoModal({ id: 40, name: "movie.mkv", size: 1024 });
        await nextTasks();
        openSettings("speed");
        const slider = document.querySelector<HTMLInputElement>("#video-speed-slider");
        expect(slider).toBeTruthy();
        if (!slider) return;
        expect(slider.min).toBe("0.25");
        expect(slider.max).toBe("4");
        slider.focus();
        slider.value = "1.75";
        slider.dispatchEvent(new Event("input", { bubbles: true }));
        await vi.waitFor(() => expect(apiMocks.nativeMediaCommand).toHaveBeenCalledWith(MKV_SESSION_ID, ["set", "speed", "1.75"]));
        expect(slider.getAttribute("aria-valuetext")).toBe("1.75 times");
        expect(document.querySelector("#video-speed-value")?.textContent).toBe("1.75x");
        expect(document.querySelector<HTMLElement>("#video-settings-panel")?.hidden).toBe(false);
        const arrow = new KeyboardEvent("keydown", { key: "ArrowRight", bubbles: true, cancelable: true });
        slider.dispatchEvent(arrow);
        expect(arrow.defaultPrevented).toBe(false);
        expect(document.activeElement).toBe(slider);
        slider.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
        expect(document.querySelector<HTMLElement>("#video-settings-panel")?.hidden).toBe(true);
        expect(document.activeElement?.id).toBe("video-picture-button");
        await videoModule.closeVideoModal();
    });
});
