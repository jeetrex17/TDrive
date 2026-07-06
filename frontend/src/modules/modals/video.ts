import {
    closeMedia,
    closeNativeMedia,
    getMediaStats,
    hideNativeSeekThumbnail,
    moveNativeSeekThumbnail,
    nativeMediaCommand,
    openMedia,
    openNativeMedia,
    resizeNativeMedia,
    showNativeSeekThumbnail,
    updateMediaPlayback,
    type MediaStats,
    type MediaOpenResult,
    type NativeMediaOpenResult,
    type NativeMediaRect,
} from "../../api";
import { EventsOn, WindowFullscreen, WindowIsFullscreen, WindowUnfullscreen } from "../../../wailsjs/runtime/runtime";
import { formatBytes } from "../../utils";
import { isWebviewDirectVideo, videoFormatLabel } from "../media-types";
import { installModalA11y } from "../../ui/modals/modal-a11y";

const CHROME_HIDE_DELAY_MS = 2500;
const LOADING_DEBOUNCE_MS = 250;
const SEEK_STEP_SECONDS = 10;
const VOLUME_STEP = 0.05;
const RATE_OPTIONS = [0.5, 0.75, 1, 1.25, 1.5, 2];
const THUMBNAIL_BUCKET_SECONDS = 10;
const THUMBNAIL_LONG_BUCKET_SECONDS = 20;
const THUMBNAIL_VERY_LONG_BUCKET_SECONDS = 30;
const THUMBNAIL_REQUEST_DEBOUNCE_MS = 140;
const THUMBNAIL_DWELL_PREFETCH_MS = 420;
const THUMBNAIL_RETRY_MS = 650;
const THUMBNAIL_FAILURE_TTL_MS = 15_000;
// When the exact bucket isn't ready yet, show the nearest already-cached frame
// within this many seconds as a placeholder (the exact frame swaps in on load).
// Kept small so the placeholder is genuinely the same scene, never a far one.
const THUMBNAIL_NEAREST_MAX_SECONDS = 120;
const PLAYBACK_HINT_INTERVAL_MS = 1000;
const MEDIA_STATS_POLL_MS = 1000;
const STREAM_ACTIVITY_HOLD_MS = 2000;

interface VideoOpenTarget {
    id: number;
    name: string;
    size?: number;
    encrypted?: boolean;
}

interface BufferedRange {
    start: number;
    end: number;
}

interface PlayerState {
    paused: boolean;
    currentTime: number;
    duration: number;
    buffered: BufferedRange[];
    volume: number;
    muted: boolean;
    rate: number;
    loading: boolean;
}

interface NativeMediaStatePayload {
    token?: string;
    paused?: boolean;
    current_time?: number;
    duration?: number;
    buffered?: BufferedRange[];
    volume?: number;
    muted?: boolean;
    rate?: number;
    loading?: boolean;
}

interface PlayerAdapter {
    subscribe(callback: (state: PlayerState) => void): () => void;
    playPause(): void;
    seekAbsolute(seconds: number): void;
    seekRelative(seconds: number): void;
    setVolume(value: number): void;
    setMuted(value: boolean): void;
    setSpeed(value: number): void;
    close(): Promise<void>;
}

const EMPTY_STATE: PlayerState = {
    paused: true,
    currentTime: 0,
    duration: 0,
    buffered: [],
    volume: 1,
    muted: false,
    rate: 1,
    loading: false,
};

class HtmlVideoAdapter implements PlayerAdapter {
    private subscribers = new Set<(state: PlayerState) => void>();
    private listeners: Array<() => void> = [];
    private closed = false;
    private lastAudibleVolume: number;

    constructor(private readonly video: HTMLVideoElement, private readonly opened: MediaOpenResult) {
        this.lastAudibleVolume = video.volume > 0 ? video.volume : 1;
        const events = [
            "loadstart",
            "loadedmetadata",
            "canplay",
            "waiting",
            "playing",
            "pause",
            "timeupdate",
            "durationchange",
            "progress",
            "volumechange",
            "ratechange",
            "seeking",
            "seeked",
        ];
        for (const event of events) {
            const listener = () => this.emit();
            video.addEventListener(event, listener);
            this.listeners.push(() => video.removeEventListener(event, listener));
        }
    }

    load() {
        this.video.pause();
        this.video.removeAttribute("src");
        this.video.load();
        this.video.src = this.opened.url;
        this.video.playbackRate = 1;
        this.emit();

        const playPromise = this.video.play();
        if (playPromise && typeof playPromise.catch === "function") {
            playPromise.catch(() => {
                // Autoplay may be blocked; leave the first frame and explicit play control visible.
                this.emit();
                revealChrome();
            });
        }
    }

    subscribe(callback: (state: PlayerState) => void) {
        this.subscribers.add(callback);
        callback(this.snapshot());
        return () => this.subscribers.delete(callback);
    }

    playPause() {
        if (this.video.paused) {
            this.video.play().catch((err) => setError(String(err?.message || err || "Playback failed.")));
        } else {
            this.video.pause();
        }
        this.emit();
    }

    seekAbsolute(seconds: number) {
        const duration = Number.isFinite(this.video.duration) ? this.video.duration : 0;
        if (duration <= 0) return;
        this.video.currentTime = clamp(seconds, 0, duration);
        this.emit();
    }

    seekRelative(seconds: number) {
        this.seekAbsolute(this.video.currentTime + seconds);
    }

    setVolume(value: number) {
        const next = clamp(value, 0, 1);
        this.video.volume = next;
        if (next > 0) this.lastAudibleVolume = next;
        this.video.muted = next === 0;
        this.emit();
    }

    setMuted(value: boolean) {
        if (!value && this.video.volume === 0) {
            this.video.volume = this.lastAudibleVolume;
        }
        this.video.muted = value;
        this.emit();
    }

    setSpeed(value: number) {
        this.video.playbackRate = clamp(value, 0.25, 4);
        this.emit();
    }

    async close() {
        if (this.closed) return;
        this.closed = true;
        for (const remove of this.listeners.splice(0)) remove();
        this.subscribers.clear();
        this.video.pause();
        this.video.removeAttribute("src");
        this.video.load();
        await closeMedia(this.opened.token);
    }

    private snapshot(): PlayerState {
        const duration = Number.isFinite(this.video.duration) ? this.video.duration : 0;
        const currentTime = Number.isFinite(this.video.currentTime) ? this.video.currentTime : 0;
        return {
            paused: this.video.paused,
            currentTime,
            duration,
            buffered: bufferedRanges(this.video),
            volume: this.video.volume,
            muted: this.video.muted || this.video.volume === 0,
            rate: this.video.playbackRate || 1,
            loading: this.video.readyState < HTMLMediaElement.HAVE_FUTURE_DATA && !this.video.paused,
        };
    }

    private emit() {
        if (this.closed) return;
        const state = this.snapshot();
        for (const callback of this.subscribers) callback(state);
    }
}

class NativeMpvAdapter implements PlayerAdapter {
    private subscribers = new Set<(state: PlayerState) => void>();
    private state: PlayerState;
    private closed = false;
    private unsubscribeRuntime: (() => void) | null = null;
    private commandFlushFrame = 0;
    private pendingSeek: { mode: "absolute" | "relative"; value: number } | null = null;
    private pendingLatestCommands = new Map<string, string[]>();
    private lastAudibleVolume = 1;

    constructor(private readonly opened: NativeMediaOpenResult) {
        this.state = { ...EMPTY_STATE, paused: false, loading: true };
        this.unsubscribeRuntime = EventsOn("native_media_state", (payload: NativeMediaStatePayload) => {
            if (this.closed || payload?.token !== this.opened.token) return;
            this.state = nativePayloadToState(payload, this.state);
            if (!this.state.muted && this.state.volume > 0) this.lastAudibleVolume = this.state.volume;
            this.emit();
        });
    }

    subscribe(callback: (state: PlayerState) => void) {
        this.subscribers.add(callback);
        callback(this.state);
        return () => this.subscribers.delete(callback);
    }

    playPause() {
        void this.sendCommand(["cycle", "pause"]);
        this.updateFallbackState((state) => ({ ...state, paused: !state.paused }));
    }

    seekAbsolute(_seconds: number) {
        if (this.state.duration <= 0) return;
        const next = clamp(_seconds, 0, Math.max(0, this.state.duration || _seconds));
        this.scheduleSeek("absolute", next);
        this.updateFallbackState((state) => ({
            ...state,
            currentTime: clamp(next, 0, Math.max(0, state.duration || next)),
            buffered: keepUsefulBufferedRanges(state.buffered, next),
        }));
    }

    seekRelative(seconds: number) {
        this.scheduleSeek("relative", seconds);
        this.updateFallbackState((state) => {
            if (state.duration <= 0) return state;
            const currentTime = clamp(state.currentTime + seconds, 0, state.duration);
            return { ...state, currentTime, buffered: keepUsefulBufferedRanges(state.buffered, currentTime) };
        });
    }

    setVolume(value: number) {
        const next = clamp(value, 0, 1);
        this.scheduleLatestCommand("volume", ["set", "volume", String(Math.round(next * 100))]);
        if (next > 0) {
            this.lastAudibleVolume = next;
            this.scheduleLatestCommand("mute", ["set", "mute", "no"]);
        }
        this.updateFallbackState((state) => ({ ...state, volume: next, muted: next <= 0 }));
    }

    setMuted(value: boolean) {
        const nextVolume = !value && this.state.volume === 0 ? this.lastAudibleVolume : this.state.volume;
        if (!value && this.state.volume === 0) {
            this.scheduleLatestCommand("volume", ["set", "volume", String(Math.round(nextVolume * 100))]);
        }
        this.scheduleLatestCommand("mute", ["set", "mute", value ? "yes" : "no"]);
        this.updateFallbackState((state) => ({ ...state, volume: nextVolume, muted: value }));
    }

    setSpeed(value: number) {
        const next = clamp(value, 0.25, 4);
        this.scheduleLatestCommand("speed", ["set", "speed", String(next)]);
        this.updateFallbackState((state) => ({ ...state, rate: next }));
    }

    async close() {
        if (this.closed) return;
        this.closed = true;
        this.clearScheduledCommands();
        this.unsubscribeRuntime?.();
        this.unsubscribeRuntime = null;
        this.subscribers.clear();
        await closeNativeMedia(this.opened.token);
    }

    private emit() {
        for (const callback of this.subscribers) callback(this.state);
    }

    private updateFallbackState(update: (state: PlayerState) => PlayerState) {
        if (this.closed) return;
        this.state = update(this.state);
        if (!this.state.muted && this.state.volume > 0) this.lastAudibleVolume = this.state.volume;
        this.emit();
    }

    private scheduleSeek(mode: "absolute" | "relative", value: number) {
        if (mode === "relative" && this.pendingSeek?.mode === "relative") {
            this.pendingSeek.value += value;
        } else {
            this.pendingSeek = { mode, value };
        }
        this.scheduleCommandFlush();
    }

    private scheduleLatestCommand(key: string, command: string[]) {
        this.pendingLatestCommands.set(key, command);
        this.scheduleCommandFlush();
    }

    private scheduleCommandFlush() {
        if (this.commandFlushFrame || this.closed) return;
        this.commandFlushFrame = requestAnimationFrame(() => {
            this.commandFlushFrame = 0;
            this.flushScheduledCommands();
        });
    }

    private flushScheduledCommands() {
        if (this.closed) return;
        const seek = this.pendingSeek;
        this.pendingSeek = null;
        if (seek) {
            void this.sendCommand(["seek", String(seek.value), seek.mode]);
        }
        const commands = Array.from(this.pendingLatestCommands.values());
        this.pendingLatestCommands.clear();
        for (const command of commands) {
            void this.sendCommand(command);
        }
    }

    private clearScheduledCommands() {
        if (this.commandFlushFrame) {
            cancelAnimationFrame(this.commandFlushFrame);
            this.commandFlushFrame = 0;
        }
        this.pendingSeek = null;
        this.pendingLatestCommands.clear();
    }

    private async sendCommand(command: string[]) {
        if (this.closed) return;
        try {
            await nativeMediaCommand(this.opened.token, command);
        } catch (err) {
            console.warn("NativeMediaCommand failed:", err);
        }
    }
}

function keepUsefulBufferedRanges(ranges: BufferedRange[], currentTime: number) {
    return ranges.filter((range) => range.end >= currentTime - 2);
}

let modalEl: HTMLElement | null = null;
let stageEl: HTMLElement | null = null;
let topbarEl: HTMLElement | null = null;
let controlsEl: HTMLElement | null = null;
let filenameEl: HTMLElement | null = null;
let metaEl: HTMLElement | null = null;
let closeBtnEl: HTMLButtonElement | null = null;
let nativeViewportEl: HTMLElement | null = null;
let videoEl: HTMLVideoElement | null = null;
let loadingEl: HTMLElement | null = null;
let loadingStatusEl: HTMLElement | null = null;
let errorEl: HTMLElement | null = null;
let centerControlsEl: HTMLElement | null = null;
let centerPlayBtnEl: HTMLButtonElement | null = null;
let centerSkipBackBtnEl: HTMLButtonElement | null = null;
let centerSkipForwardBtnEl: HTMLButtonElement | null = null;
let skipFeedbackEl: HTMLElement | null = null;
let playBtnEl: HTMLButtonElement | null = null;
let skipBackBtnEl: HTMLButtonElement | null = null;
let skipForwardBtnEl: HTMLButtonElement | null = null;
let muteBtnEl: HTMLButtonElement | null = null;
let fullscreenBtnEl: HTMLButtonElement | null = null;
let scrubberEl: HTMLElement | null = null;
let scrubberPlayedEl: HTMLElement | null = null;
let scrubberBufferedEl: HTMLElement | null = null;
let scrubberThumbEl: HTMLElement | null = null;
let scrubberTooltipEl: HTMLElement | null = null;
let scrubberTooltipImageEl: HTMLImageElement | null = null;
let scrubberTooltipTimeEl: HTMLElement | null = null;
let volumeSliderEl: HTMLElement | null = null;
let volumeFillEl: HTMLElement | null = null;
let volumeThumbEl: HTMLElement | null = null;
let timeEl: HTMLElement | null = null;
let durationEl: HTMLElement | null = null;
let speedBtnEl: HTMLButtonElement | null = null;
let speedMenuEl: HTMLElement | null = null;

let activeAdapter: PlayerAdapter | null = null;
let activeNative: NativeMediaOpenResult | null = null;
let activeMediaToken = "";
let unsubscribeState: (() => void) | null = null;
let currentState: PlayerState = { ...EMPTY_STATE };
let openSeq = 0;
let chromeHideTimer: ReturnType<typeof setTimeout> | null = null;
let loadingTimer: ReturnType<typeof setTimeout> | null = null;
let skipFeedbackTimer: ReturnType<typeof setTimeout> | null = null;
let nativeResizeFrame = 0;
let seekingWithPointer = false;
let volumeDragging = false;
let pendingVolumeValue: number | null = null;
let volumeCommandFrame = 0;
let hasError = false;
let isWindowFullscreen = false;
let lastBufferedSignature = "";
let activeThumbnailURL = "";
let currentPreviewBucket = -1;
let lastPreviewRatio = 0;
let thumbnailRequestSeq = 0;
let thumbnailRequestTimer: number | null = null;
let thumbnailDwellTimer: number | null = null;
let scheduledThumbnailBucket = -1;
let lastPlaybackHintAt = 0;
let playbackHintTimer: number | null = null;
let playbackHintInFlight = false;
let mediaStatsTimer: number | null = null;
let mediaStatsInFlight = false;
let streamActivityClearTimer: number | null = null;
let streamActivityText = "";
let streamActivityAt = 0;
let mediaMetaBaseText = "";
let mediaMetaBytes = 0;
const thumbnailObjectURLs = new Map<number, string>();
// Native fallback only: base64 of frames already shown, so the seek overlay is
// not re-encoded on every scrub. nativeSeekAspect tracks the loaded thumbnail's
// aspect ratio so the overlay box is not distorted.
const thumbnailBase64 = new Map<number, string>();
let nativeSeekAspect = 9 / 16;
const pendingThumbnails = new Set<number>();
const failedThumbnails = new Map<number, number>();
let a11y: ReturnType<typeof installModalA11y> | null = null;

// Gap between the native video and the chrome strips. Kept small so the picture
// is as large as possible; the chrome itself is measured, so this is the only
// slack. Sides are trimmed too — horizontal margin only shrinks the picture.
const FALLBACK_NATIVE_GAP_PX = 4;
const FALLBACK_NATIVE_SIDE_PX = 0;
const FALLBACK_NATIVE_SIDE_COMPACT_PX = 0;

function byID<T extends HTMLElement>(id: string): T | null {
    return document.getElementById(id) as T | null;
}

function clamp(value: number, min: number, max: number) {
    return Math.max(min, Math.min(max, Number.isFinite(value) ? value : min));
}

function percent(value: number, total: number) {
    return total > 0 ? clamp((value / total) * 100, 0, 100) : 0;
}

// Fragment boundaries (e.g. fMP4 segments) can split one contiguous download
// into several adjacent ranges; rendering every hairline gap is visual noise,
// not honesty, so segments closer than this are fused for display.
const BUFFERED_MERGE_GAP_SECONDS = 0.4;

// bufferedRanges reads the full set of cached [start, end] segments from the
// media element. HTML video keeps several disjoint ranges after seeking, so we
// preserve all of them instead of collapsing to a single "buffered end" — the
// timeline can then honestly show which parts are actually downloaded.
function bufferedRanges(video: HTMLVideoElement): BufferedRange[] {
    const ranges: BufferedRange[] = [];
    const buffered = video.buffered;
    for (let i = 0; i < buffered.length; i += 1) {
        const start = buffered.start(i);
        const end = buffered.end(i);
        if (Number.isFinite(start) && Number.isFinite(end) && end > start) {
            ranges.push({ start, end });
        }
    }
    return ranges;
}

function nativePayloadToState(payload: NativeMediaStatePayload, previous: PlayerState): PlayerState {
    const duration = Number(payload.duration ?? previous.duration ?? 0);
    const currentTime = Number(payload.current_time ?? previous.currentTime ?? 0);
    const volume = Number(payload.volume ?? previous.volume ?? 1);
    const rate = Number(payload.rate ?? previous.rate ?? 1);
    const buffered = Array.isArray(payload.buffered)
        ? payload.buffered
            .map((range) => ({
                start: Number(range.start ?? 0),
                end: Number(range.end ?? 0),
            }))
            .filter((range) => Number.isFinite(range.start) && Number.isFinite(range.end) && range.end > range.start)
        : previous.buffered;
    return {
        paused: Boolean(payload.paused ?? previous.paused),
        currentTime: clamp(currentTime, 0, Math.max(0, duration || currentTime)),
        duration: Math.max(0, Number.isFinite(duration) ? duration : 0),
        buffered,
        volume: clamp(volume, 0, 1),
        muted: Boolean(payload.muted ?? previous.muted),
        rate: clamp(rate, 0.25, 4),
        loading: Boolean(payload.loading ?? false),
    };
}

// coalesceRanges clamps ranges to the known duration and merges any whose gap is
// within BUFFERED_MERGE_GAP_SECONDS, returning a sorted, disjoint set ready to
// paint. The native adapter can feed the same shape later from mpv's cache state.
function coalesceRanges(ranges: BufferedRange[], duration: number): BufferedRange[] {
    const sorted = ranges
        .map((range) => ({ start: clamp(range.start, 0, duration), end: clamp(range.end, 0, duration) }))
        .filter((range) => range.end > range.start)
        .sort((a, b) => a.start - b.start);
    const merged: BufferedRange[] = [];
    for (const range of sorted) {
        const last = merged[merged.length - 1];
        if (last && range.start - last.end <= BUFFERED_MERGE_GAP_SECONDS) {
            last.end = Math.max(last.end, range.end);
        } else {
            merged.push({ ...range });
        }
    }
    return merged;
}

function isOpen() {
    return Boolean(modalEl && modalEl.style.display !== "none");
}

function errorMessage(err: unknown, fallback: string) {
    const raw = err instanceof Error && err.message ? err.message : String(err || "");
    const normalized = raw.toLowerCase();
    if (
        normalized.includes("resolve peer") ||
        normalized.includes("rpcdorequest") ||
        normalized.includes("retryuntilack") ||
        normalized.includes("engine forcibly closed")
    ) {
        return "Could not reach Telegram. Check your connection and try again.";
    }
    if (normalized.includes("context canceled")) {
        return "The video request was canceled. Try opening it again.";
    }
    return raw || fallback;
}

function isNativeFallbackActive() {
    return Boolean(activeNative && !activeNative.htmlControls);
}

function setChromeVisible(visible: boolean) {
    modalEl?.classList.toggle("is-video-chrome-visible", visible);
    modalEl?.classList.toggle("is-video-cursor-hidden", !visible && !hasError && !isNativeFallbackActive());
}

function setNativeMode(visible: boolean, fallback = false) {
    modalEl?.classList.toggle("is-video-native", visible);
    modalEl?.classList.toggle("is-video-native-fallback", visible && fallback);
    document.body.classList.toggle("native-video-active", visible && !fallback);
    syncFallbackNativeViewportInsets();
}

function shouldMeasureNativeFallbackBeforeOpen() {
    // macOS renders libmpv below a transparent WebView and can overlay HTML controls.
    // Windows/Linux use native child windows above the WebView, so reserve HTML-owned
    // top/bottom strips before opening mpv or it can briefly cover the whole modal.
    return !/macintosh|mac os x/i.test(window.navigator.userAgent);
}

function fullscreenRuntimeAvailable() {
    return Boolean(
        window.runtime?.WindowFullscreen &&
        window.runtime?.WindowUnfullscreen &&
        window.runtime?.WindowIsFullscreen
    );
}

function canUseFullscreen() {
    return Boolean(fullscreenRuntimeAvailable() && activeAdapter && !hasError);
}

function applyFullscreenState(isFullscreen: boolean) {
    isWindowFullscreen = isFullscreen;
    modalEl?.classList.toggle("is-video-fullscreen", isWindowFullscreen);
    if (!fullscreenBtnEl) return;
    fullscreenBtnEl.dataset.state = isWindowFullscreen ? "fullscreen" : "windowed";
    fullscreenBtnEl.setAttribute("aria-label", isWindowFullscreen ? "Exit fullscreen" : "Enter fullscreen");
    fullscreenBtnEl.title = isWindowFullscreen ? "Exit fullscreen" : "Enter fullscreen";
    setButtonDisabled(fullscreenBtnEl, !canUseFullscreen());
}

async function readWindowFullscreen() {
    if (!fullscreenRuntimeAvailable()) return false;
    try {
        return Boolean(await WindowIsFullscreen());
    } catch (err) {
        console.warn("WindowIsFullscreen failed:", err);
        return false;
    }
}

async function syncFullscreenState() {
    applyFullscreenState(await readWindowFullscreen());
}

async function exitVideoFullscreen() {
    if (!fullscreenRuntimeAvailable()) return;
    if (!(await readWindowFullscreen())) return;
    try {
        WindowUnfullscreen();
        applyFullscreenState(false);
    } catch (err) {
        console.warn("WindowUnfullscreen failed:", err);
    }
}

async function toggleFullscreen() {
    if (!canUseFullscreen()) return;
    try {
        const next = !(await readWindowFullscreen());
        if (next) {
            WindowFullscreen();
        } else {
            WindowUnfullscreen();
        }
        applyFullscreenState(next);
    } catch (err) {
        console.warn("toggle fullscreen failed:", err);
    } finally {
        scheduleNativeResizeAfterWindowTransition();
        setTimeout(() => {
            void syncFullscreenState();
            scheduleNativeResizeAfterWindowTransition();
        }, 180);
        revealChrome();
    }
}

function nextFrame(): Promise<void> {
    return new Promise((resolve) => requestAnimationFrame(() => resolve()));
}

function syncFallbackNativeViewportInsets() {
    if (!modalEl || !stageEl || !nativeViewportEl || !modalEl.classList.contains("is-video-native-fallback")) return;
    const stageRect = stageEl.getBoundingClientRect();
    if (stageRect.width <= 0 || stageRect.height <= 0) return;

    const topbarRect = topbarEl?.getBoundingClientRect();
    const controlsRect = controlsEl?.getBoundingClientRect();
    const compact = window.matchMedia("(max-width: 760px)").matches;
    const side = compact ? FALLBACK_NATIVE_SIDE_COMPACT_PX : FALLBACK_NATIVE_SIDE_PX;
    const topbarBottom = topbarRect ? Math.max(topbarRect.bottom, stageRect.top) : stageRect.top;
    const controlsTop = controlsRect ? Math.min(controlsRect.top, stageRect.bottom) : stageRect.bottom;
    const top = Math.ceil(Math.max(0, topbarBottom - stageRect.top) + FALLBACK_NATIVE_GAP_PX);
    const bottom = Math.ceil(Math.max(0, stageRect.bottom - controlsTop) + FALLBACK_NATIVE_GAP_PX);

    nativeViewportEl.style.setProperty("--video-native-side-inset", `${side}px`);
    nativeViewportEl.style.setProperty("--video-native-top-inset", `${top}px`);
    nativeViewportEl.style.setProperty("--video-native-bottom-inset", `${bottom}px`);
}

function currentNativeRect(): NativeMediaRect | null {
    syncFallbackNativeViewportInsets();
    const source = nativeViewportEl || stageEl;
    if (!source) return null;
    const rect = source.getBoundingClientRect();
    if (rect.width < 2 || rect.height < 2) return null;
    return {
        x: rect.left,
        y: rect.top,
        width: rect.width,
        height: rect.height,
    };
}

function scheduleNativeResize() {
    if (!activeNative) return;
    if (nativeResizeFrame) cancelAnimationFrame(nativeResizeFrame);
    nativeResizeFrame = requestAnimationFrame(() => {
        nativeResizeFrame = 0;
        const token = activeNative?.token || "";
        const rect = currentNativeRect();
        if (!token || !rect) return;
        void resizeNativeMedia(token, rect).catch((err) => {
            console.warn("ResizeNativeMedia failed:", err);
        });
    });
}

function scheduleNativeResizeAfterWindowTransition() {
    if (!activeNative) return;
    scheduleNativeResize();
    requestAnimationFrame(scheduleNativeResize);
    window.setTimeout(scheduleNativeResize, 180);
    window.setTimeout(scheduleNativeResize, 420);
}

function clearChromeTimer() {
    if (!chromeHideTimer) return;
    clearTimeout(chromeHideTimer);
    chromeHideTimer = null;
}

function scheduleChromeHide() {
    clearChromeTimer();
    if (!isOpen() || currentState.paused || hasError || isSpeedMenuOpen() || isScrubberTooltipActive()) return;
    chromeHideTimer = setTimeout(() => {
        if (!isOpen() || currentState.paused || hasError || isSpeedMenuOpen() || isScrubberTooltipActive()) return;
        setChromeVisible(false);
    }, CHROME_HIDE_DELAY_MS);
}

function isScrubberTooltipActive() {
    return Boolean(scrubberEl?.classList.contains("is-hovered") || currentPreviewBucket >= 0);
}

function revealChrome() {
    if (!isOpen()) return;
    setChromeVisible(true);
    scheduleChromeHide();
}

function setLoading(visible: boolean) {
    if (!loadingEl) return;
    modalEl?.classList.toggle("is-video-loading", visible);
    if (loadingTimer) {
        clearTimeout(loadingTimer);
        loadingTimer = null;
    }
    if (!visible) {
        loadingEl.style.display = "none";
        loadingEl.setAttribute("aria-hidden", "true");
        updateLoadingStatus();
        return;
    }
    updateLoadingStatus();
    loadingTimer = setTimeout(() => {
        loadingTimer = null;
        if (!loadingEl || hasError) return;
        loadingEl.style.display = "flex";
        loadingEl.setAttribute("aria-hidden", "false");
    }, LOADING_DEBOUNCE_MS);
}

function updateLoadingStatus() {
    if (!loadingStatusEl) return;
    if (!activeAdapter) {
        loadingStatusEl.textContent = "Opening video";
        return;
    }
    if (streamActivityText === "Rate-limited") {
        loadingStatusEl.textContent = "Buffering · Rate-limited";
        return;
    }
    if (streamActivityText.startsWith("Streaming ")) {
        loadingStatusEl.textContent = `Buffering · ${streamActivityText.slice("Streaming ".length)}`;
        return;
    }
    loadingStatusEl.textContent = "Buffering";
}

function syncMediaStatsPolling() {
    if (activeMediaToken && !hasError) {
        startMediaStatsPolling();
    } else {
        clearMediaStatsPolling();
    }
}

function startMediaStatsPolling() {
    if (mediaStatsTimer != null) return;
    void pollMediaStats();
    mediaStatsTimer = window.setInterval(() => {
        void pollMediaStats();
    }, MEDIA_STATS_POLL_MS);
}

function clearMediaStatsPolling() {
    if (mediaStatsTimer != null) {
        window.clearInterval(mediaStatsTimer);
        mediaStatsTimer = null;
    }
    clearStreamActivity();
    updateLoadingStatus();
}

async function pollMediaStats() {
    if (!activeMediaToken || mediaStatsInFlight) return;
    const token = activeMediaToken;
    mediaStatsInFlight = true;
    try {
        const stats = await getMediaStats(token);
        if (token !== activeMediaToken) return;
        syncStreamActivity(stats);
    } catch (err) {
        console.warn("GetMediaStats failed:", err);
    } finally {
        mediaStatsInFlight = false;
    }
}

function syncStreamActivity(stats: MediaStats | null) {
    const text = stats ? streamActivityLabel(stats) : "";
    const now = Date.now();
    if (text) {
        streamActivityText = text;
        streamActivityAt = now;
        scheduleStreamActivityClear();
    } else if (streamActivityText && now - streamActivityAt >= STREAM_ACTIVITY_HOLD_MS) {
        streamActivityText = "";
        clearStreamActivityTimer();
    }
    renderMediaMeta();
    updateLoadingStatus();
}

function streamActivityLabel(stats: MediaStats) {
    const playback = stats.playback;
    if (playback.recentFloodWait) {
        return "Rate-limited";
    }
    const rate = playback.bytesPerSecond || 0;
    if (rate <= 0) return "";
    const multiplier = formatStreamMultiplier(rate);
    return `Streaming ${formatStreamRate(rate)}${multiplier ? ` ${multiplier}` : ""}`;
}

function scheduleStreamActivityClear() {
    clearStreamActivityTimer();
    streamActivityClearTimer = window.setTimeout(() => {
        if (Date.now() - streamActivityAt >= STREAM_ACTIVITY_HOLD_MS) {
            streamActivityText = "";
            renderMediaMeta();
            updateLoadingStatus();
        }
        streamActivityClearTimer = null;
    }, STREAM_ACTIVITY_HOLD_MS);
}

function clearStreamActivityTimer() {
    if (streamActivityClearTimer == null) return;
    window.clearTimeout(streamActivityClearTimer);
    streamActivityClearTimer = null;
}

function clearStreamActivity() {
    clearStreamActivityTimer();
    streamActivityText = "";
    streamActivityAt = 0;
    renderMediaMeta();
}

function showSkipFeedback(delta: number) {
    if (!skipFeedbackEl) return;
    const value = Math.abs(Math.round(delta));
    const label = `${delta > 0 ? "+" : "-"}${value}s`;
    const textEl = skipFeedbackEl.querySelector("span");
    if (textEl) textEl.textContent = label;

    if (skipFeedbackTimer) {
        clearTimeout(skipFeedbackTimer);
        skipFeedbackTimer = null;
    }
    skipFeedbackEl.classList.remove("is-visible", "is-forward", "is-back");
    // Restart the pulse when repeated skips happen quickly.
    void skipFeedbackEl.offsetWidth;
    skipFeedbackEl.classList.add(delta > 0 ? "is-forward" : "is-back", "is-visible");
    skipFeedbackTimer = setTimeout(() => {
        skipFeedbackTimer = null;
        skipFeedbackEl?.classList.remove("is-visible");
    }, 620);
}

function clearSkipFeedback() {
    if (skipFeedbackTimer) {
        clearTimeout(skipFeedbackTimer);
        skipFeedbackTimer = null;
    }
    skipFeedbackEl?.classList.remove("is-visible", "is-forward", "is-back");
}

function setError(message: string) {
    hasError = true;
    setLoading(false);
    clearMediaStatsPolling();
    if (!errorEl) return;
    errorEl.textContent = message;
    errorEl.style.display = "block";
    modalEl?.classList.add("is-video-error");
    setChromeVisible(true);
    clearChromeTimer();
}

function clearError() {
    hasError = false;
    if (!errorEl) return;
    errorEl.textContent = "";
    errorEl.style.display = "none";
    modalEl?.classList.remove("is-video-error");
}

function formatTime(value: number) {
    if (!Number.isFinite(value) || value < 0) return "0:00";
    const total = Math.floor(value);
    const hours = Math.floor(total / 3600);
    const minutes = Math.floor((total % 3600) / 60);
    const seconds = total % 60;
    if (hours > 0) {
        return `${hours}:${String(minutes).padStart(2, "0")}:${String(seconds).padStart(2, "0")}`;
    }
    return `${minutes}:${String(seconds).padStart(2, "0")}`;
}

function setSliderARIA(el: HTMLElement | null, value: number, min: number, max: number, text: string) {
    if (!el) return;
    el.setAttribute("aria-valuemin", String(min));
    el.setAttribute("aria-valuemax", String(max));
    el.setAttribute("aria-valuenow", String(Math.round(value)));
    el.setAttribute("aria-valuetext", text);
}

function setButtonDisabled(button: HTMLButtonElement | null, disabled: boolean) {
    if (!button) return;
    button.disabled = disabled;
    button.setAttribute("aria-disabled", disabled ? "true" : "false");
}

function setSliderDisabled(el: HTMLElement | null, disabled: boolean) {
    if (!el) return;
    el.classList.toggle("is-disabled", disabled);
    el.setAttribute("aria-disabled", disabled ? "true" : "false");
    el.tabIndex = disabled ? -1 : 0;
}

function syncTransportAvailability(state: PlayerState) {
    const canScrub = Boolean(activeAdapter && (state.duration > 0 || isNativeFallbackActive()));
    const canRelativeSeek = canScrub || isNativeFallbackActive();
    setButtonDisabled(skipBackBtnEl, !canRelativeSeek);
    setButtonDisabled(skipForwardBtnEl, !canRelativeSeek);
    setButtonDisabled(centerSkipBackBtnEl, !canRelativeSeek);
    setButtonDisabled(centerSkipForwardBtnEl, !canRelativeSeek);
    setSliderDisabled(scrubberEl, !canScrub);
    applyFullscreenState(isWindowFullscreen);
}

function syncCenterPlay(state: PlayerState) {
    const visible = Boolean(activeAdapter && state.paused && !state.loading && !hasError);
    modalEl?.classList.toggle("is-video-paused", visible);
    centerControlsEl?.setAttribute("aria-hidden", visible ? "false" : "true");
}

function syncButtonState(state: PlayerState) {
    if (!playBtnEl || !muteBtnEl) return;
    playBtnEl.dataset.state = state.paused ? "paused" : "playing";
    playBtnEl.setAttribute("aria-label", state.paused ? "Play" : "Pause");
    playBtnEl.title = state.paused ? "Play" : "Pause";
    muteBtnEl.dataset.state = state.muted ? "muted" : "unmuted";
    muteBtnEl.setAttribute("aria-label", state.muted ? "Unmute" : "Mute");
    muteBtnEl.title = state.muted ? "Unmute" : "Mute";
}

function syncTimeline(state: PlayerState) {
    if (timeEl) timeEl.textContent = formatTime(state.currentTime);
    if (durationEl) durationEl.textContent = state.duration > 0 ? formatTime(state.duration) : "--:--";

    const played = percent(state.currentTime, state.duration);
    if (scrubberPlayedEl && !seekingWithPointer) scrubberPlayedEl.style.width = `${played}%`;
    if (scrubberThumbEl && !seekingWithPointer) scrubberThumbEl.style.left = `${played}%`;
    renderBuffered(state);
    if (scrubberEl) {
        setSliderARIA(scrubberEl, state.currentTime, 0, Math.max(0, state.duration), `${formatTime(state.currentTime)} of ${state.duration > 0 ? formatTime(state.duration) : "unknown"}`);
    }
}

// renderBuffered paints one segment per cached range so the bar honestly shows
// the disjoint downloaded islands (not a single block to the furthest point).
// It is diff-guarded: buffered only changes on the media element's "progress"
// event, but applyState also runs on every "timeupdate", so we skip DOM work
// whenever the rendered set is unchanged. Releasing a session resets to an empty
// state, which clears the segments and the signature for the next video.
function renderBuffered(state: PlayerState) {
    const container = scrubberBufferedEl;
    if (!container) return;

    const segments: Array<{ left: number; width: number }> = [];
    if (state.duration > 0) {
        for (const range of coalesceRanges(state.buffered, state.duration)) {
            const left = clamp((range.start / state.duration) * 100, 0, 100);
            const width = clamp(((range.end - range.start) / state.duration) * 100, 0, 100 - left);
            if (width > 0) segments.push({ left, width });
        }
    }

    const signature = segments.map((s) => `${s.left.toFixed(3)}:${s.width.toFixed(3)}`).join("|");
    if (signature === lastBufferedSignature) return;
    lastBufferedSignature = signature;

    while (container.childElementCount > segments.length) {
        container.lastElementChild?.remove();
    }
    while (container.childElementCount < segments.length) {
        const segment = document.createElement("span");
        segment.className = "video-scrubber-segment";
        container.appendChild(segment);
    }
    segments.forEach((segment, index) => {
        const node = container.children[index] as HTMLElement;
        node.style.left = `${segment.left}%`;
        node.style.width = `${segment.width}%`;
    });
}

function syncVolume(state: PlayerState) {
    const value = state.muted ? 0 : state.volume;
    previewVolume(value);
}

function previewVolume(value: number) {
    const safe = clamp(value, 0, 1);
    if (volumeFillEl) volumeFillEl.style.width = `${safe * 100}%`;
    if (volumeThumbEl) volumeThumbEl.style.left = `${safe * 100}%`;
    setSliderARIA(volumeSliderEl, safe * 100, 0, 100, `${Math.round(safe * 100)}%`);
}

function syncSpeed(state: PlayerState) {
    if (speedBtnEl) {
        speedBtnEl.textContent = `${formatRate(state.rate)}x`;
        speedBtnEl.title = isNativeFallbackActive() ? "Playback speed (click to cycle)" : "Playback speed";
        speedBtnEl.setAttribute("aria-label", speedBtnEl.title);
    }
    speedMenuEl?.querySelectorAll<HTMLButtonElement>("[data-rate]").forEach((button) => {
        const selected = Number(button.dataset.rate || 1) === state.rate;
        button.classList.toggle("is-selected", selected);
        button.setAttribute("aria-checked", selected ? "true" : "false");
    });
}

function nextPlaybackRate(currentRate: number) {
    const current = Number.isFinite(currentRate) && currentRate > 0 ? currentRate : 1;
    const currentIndex = RATE_OPTIONS.findIndex((rate) => Math.abs(rate - current) < 0.001);
    if (currentIndex >= 0) return RATE_OPTIONS[(currentIndex + 1) % RATE_OPTIONS.length];
    const nextHigher = RATE_OPTIONS.find((rate) => rate > current);
    return nextHigher ?? RATE_OPTIONS[0];
}

function cycleFallbackPlaybackRate() {
    if (!activeAdapter) return;
    const rate = nextPlaybackRate(currentState.rate);
    activeAdapter.setSpeed(rate);
    closeSpeedMenu();
    revealChrome();
}

function applyState(state: PlayerState) {
    const wasPaused = currentState.paused;
    currentState = state;
    schedulePlaybackHint(state);
    syncButtonState(state);
    syncTimeline(state);
    syncVolume(state);
    syncSpeed(state);
    syncTransportAvailability(state);
    syncCenterPlay(state);
    setLoading(state.loading);
    syncMediaStatsPolling();
    if (state.paused || hasError) {
        clearChromeTimer();
        setChromeVisible(true);
    } else if (wasPaused) {
        scheduleChromeHide();
    }
}

function schedulePlaybackHint(state: PlayerState) {
    if (!activeMediaToken || state.duration <= 0) return;
    const now = Date.now();
    const dueIn = PLAYBACK_HINT_INTERVAL_MS - (now - lastPlaybackHintAt);
    if (dueIn <= 0) {
        void sendPlaybackHint(state);
        return;
    }
    if (playbackHintTimer != null) return;
    playbackHintTimer = window.setTimeout(() => {
        playbackHintTimer = null;
        void sendPlaybackHint(currentState);
    }, dueIn);
}

async function sendPlaybackHint(state: PlayerState) {
    if (!activeMediaToken || playbackHintInFlight || state.duration <= 0) return;
    playbackHintInFlight = true;
    lastPlaybackHintAt = Date.now();
    try {
        await updateMediaPlayback({
            token: activeMediaToken,
            currentTime: state.currentTime,
            duration: state.duration,
            bufferAhead: bufferAheadSeconds(state),
        });
    } catch (err) {
        console.warn("UpdateMediaPlayback failed:", err);
    } finally {
        playbackHintInFlight = false;
    }
}

// bufferAheadSeconds reports how many seconds are buffered ahead of the current
// playhead. It reads the shared PlayerState.buffered ranges, so it works for both
// the HTML <video> and native mpv engines. The thumbnail scheduler uses it to
// decide how aggressively it may build previews without starving playback.
function bufferAheadSeconds(state: PlayerState): number {
    const t = state.currentTime;
    for (const range of state.buffered) {
        if (range.start <= t && t <= range.end) {
            return Math.max(0, range.end - t);
        }
    }
    return 0;
}

function clearPlaybackHintTimer() {
    if (playbackHintTimer == null) return;
    window.clearTimeout(playbackHintTimer);
    playbackHintTimer = null;
}

function formatRate(rate: number) {
    return Number.isInteger(rate) ? String(rate) : String(rate).replace(/0+$/, "").replace(/\.$/, "");
}

function formatStreamRate(bytesPerSecond: number) {
    const safe = Math.max(0, Number.isFinite(bytesPerSecond) ? bytesPerSecond : 0);
    if (safe < 1024 * 1024) {
        return `${Math.max(0.1, safe / 1024).toFixed(1)} KB/s`;
    }
    return `${(safe / (1024 * 1024)).toFixed(1)} MB/s`;
}

function formatStreamMultiplier(bytesPerSecond: number) {
    const duration = currentState.duration;
    if (!(mediaMetaBytes > 0 && duration > 0 && bytesPerSecond > 0)) return "";
    const averageBytesPerSecond = mediaMetaBytes / duration;
    if (!(averageBytesPerSecond > 0)) return "";
    const multiplier = bytesPerSecond / averageBytesPerSecond;
    if (!Number.isFinite(multiplier) || multiplier <= 0) return "";
    if (multiplier >= 100) return "(~99x+)";
    if (multiplier < 10) return `(~${Math.max(0.1, multiplier).toFixed(1)}x)`;
    return `(~${Math.round(multiplier)}x)`;
}

function scrubberSecondsFromEvent(event: PointerEvent | MouseEvent) {
    if (!scrubberEl || currentState.duration <= 0) return 0;
    const rect = scrubberEl.getBoundingClientRect();
    const ratio = clamp((event.clientX - rect.left) / Math.max(1, rect.width), 0, 1);
    return ratio * currentState.duration;
}

function previewScrubber(event: PointerEvent | MouseEvent) {
    if (!scrubberEl || !scrubberTooltipEl) return;
    const rect = scrubberEl.getBoundingClientRect();
    const ratio = clamp((event.clientX - rect.left) / Math.max(1, rect.width), 0, 1);
    lastPreviewRatio = ratio;
    if (currentState.duration <= 0) {
        if (isNativeFallbackActive() && activeThumbnailURL) {
            currentPreviewBucket = -1;
            if (scrubberTooltipTimeEl) scrubberTooltipTimeEl.textContent = "--:--";
            setThumbnailTooltipState("pending");
            positionScrubberTooltip(ratio, rect);
        }
        return;
    }
    const seconds = ratio * currentState.duration;
    updateThumbnailTooltip(seconds);
    positionScrubberTooltip(ratio, rect);
}

function positionScrubberTooltip(ratio: number, rect?: DOMRect) {
    if (!scrubberEl || !scrubberTooltipEl) return;
    const bounds = rect || scrubberEl.getBoundingClientRect();
    const tooltipWidth = scrubberTooltipEl.offsetWidth || 44;
    const half = tooltipWidth / 2;
    const x = clamp(ratio * bounds.width, half, Math.max(half, bounds.width - half));
    scrubberTooltipEl.style.left = `${x}px`;
}

function updateThumbnailTooltip(seconds: number) {
    if (scrubberTooltipTimeEl) scrubberTooltipTimeEl.textContent = formatTime(seconds);
    const bucket = thumbnailBucket(seconds);
    currentPreviewBucket = bucket;
    const cached = thumbnailObjectURLs.get(bucket);
    if (cached && scrubberTooltipImageEl && scrubberTooltipEl) {
        clearThumbnailRequestTimer();
        scheduleThumbnailDwell(bucket);
        showTooltipImage(cached);
        setThumbnailTooltipState("ready");
        presentNativeSeekPreview(bucket);
        return;
    }
    clearThumbnailDwellTimer();
    // No exact frame yet: show the nearest already-cached frame as a placeholder so
    // the user never stares at a blank skeleton. The time label stays exact, and the
    // precise frame swaps in when it loads (keepVisible avoids a skeleton flash).
    const nearestBucket = nearestCachedBucket(bucket);
    if (nearestBucket !== null && scrubberTooltipImageEl && scrubberTooltipEl) {
        const nearestURL = thumbnailObjectURLs.get(nearestBucket);
        if (nearestURL) showTooltipImage(nearestURL);
        setThumbnailTooltipState("ready");
        presentNativeSeekPreview(nearestBucket);
        scheduleThumbnailRequest(bucket, true);
        return;
    }
    if (thumbnailFailedRecently(bucket)) {
        setThumbnailTooltipState("failed");
        return;
    }
    setThumbnailTooltipState("pending");
    scheduleThumbnailRequest(bucket);
}

// nearestCachedBucket returns the cached frame's bucket closest to bucket, but
// only within THUMBNAIL_NEAREST_MAX_SECONDS so the placeholder is the same scene.
function nearestCachedBucket(bucket: number): number | null {
    let best: number | null = null;
    let bestDistance = Infinity;
    for (const cachedBucket of thumbnailObjectURLs.keys()) {
        const distance = Math.abs(cachedBucket - bucket);
        if (distance <= THUMBNAIL_NEAREST_MAX_SECONDS && distance < bestDistance) {
            bestDistance = distance;
            best = cachedBucket;
        }
    }
    return best;
}

function showTooltipImage(url: string) {
    if (scrubberTooltipImageEl && scrubberTooltipImageEl.src !== url) {
        scrubberTooltipImageEl.src = url;
    }
}

// --- Native seek-thumbnail overlay (Windows/Linux fallback) ---------------
// WebView2 can't paint HTML over the native video, so in the fallback the seek
// preview is drawn by a native overlay window. We hand the backend the same
// frame bytes the HTML tooltip would show plus a target box in CSS pixels, and
// throttle the calls so a fast scrub doesn't flood the bridge.

const NATIVE_SEEK_PREVIEW_WIDTH = 144;
const NATIVE_SEEK_MOVE_THROTTLE_MS = 16;
let nativeSeekThrottleTimer: number | null = null;
let nativeSeekPending: { token: string; bucket: number; image?: string; rect: NativeMediaRect } | null = null;
let nativeSeekLastShown: { token: string; bucket: number } | null = null;

function presentNativeSeekPreview(bucket: number) {
    if (!isNativeFallbackActive()) return;
    const token = activeNative?.token;
    if (!token) return;
    const cached = thumbnailBase64.get(bucket);
    if (cached) {
        const rect = nativeSeekOverlayRect();
        if (rect) queueNativeSeek(token, bucket, cached, rect);
        return;
    }
    const url = thumbnailObjectURLs.get(bucket);
    if (!url) return;
    // Encode the already-fetched frame once, then cache it so later scrubs over
    // this bucket render without re-encoding. Fetching blob: URLs is unreliable
    // in some WebView2 builds, so this is only a fallback for frames cached
    // before the native fallback path asked for their bytes.
    void objectURLToBase64(url).then((image) => {
        if (!image) return;
        thumbnailBase64.set(bucket, image);
        if (currentPreviewBucket === bucket && isNativeFallbackActive() && activeNative?.token === token) {
            const rect = nativeSeekOverlayRect();
            if (rect) queueNativeSeek(token, bucket, image, rect);
        }
    });
}

function queueNativeSeek(token: string, bucket: number, image: string, rect: NativeMediaRect) {
    const needsUpload = !nativeSeekLastShown || nativeSeekLastShown.token !== token || nativeSeekLastShown.bucket !== bucket;
    nativeSeekPending = { token, bucket, image: needsUpload ? image : undefined, rect };
    if (nativeSeekThrottleTimer !== null) return;
    flushNativeSeek(); // leading edge: show immediately
    nativeSeekThrottleTimer = window.setTimeout(() => {
        nativeSeekThrottleTimer = null;
        flushNativeSeek(); // trailing edge: show the latest position
    }, NATIVE_SEEK_MOVE_THROTTLE_MS);
}

function flushNativeSeek() {
    const req = nativeSeekPending;
    nativeSeekPending = null;
    if (!req) return;
    if (req.image) {
        nativeSeekLastShown = { token: req.token, bucket: req.bucket };
        void showNativeSeekThumbnail(req.token, req.image, req.rect);
        return;
    }
    void moveNativeSeekThumbnail(req.token, req.rect);
}

function hideNativeSeekPreview() {
    nativeSeekPending = null;
    nativeSeekLastShown = null;
    if (nativeSeekThrottleTimer !== null) {
        window.clearTimeout(nativeSeekThrottleTimer);
        nativeSeekThrottleTimer = null;
    }
    const token = activeNative?.token;
    if (token) void hideNativeSeekThumbnail(token);
}

// nativeSeekOverlayRect returns the preview box (CSS pixels, viewport coords)
// centered on the hovered point just above the scrubber. On Windows/Linux this
// is a small native child window, so it can sit over the mpv video rectangle
// while the WebView controls remain in their reserved chrome strip.
function nativeSeekOverlayRect(): NativeMediaRect | null {
    if (!scrubberEl) return null;
    const sb = scrubberEl.getBoundingClientRect();
    if (sb.width < 2) return null;
    const img = scrubberTooltipImageEl;
    if (img && img.naturalWidth > 0 && img.naturalHeight > 0) {
        nativeSeekAspect = img.naturalHeight / img.naturalWidth;
    }
    const width = NATIVE_SEEK_PREVIEW_WIDTH;
    const height = Math.max(1, Math.round(width * nativeSeekAspect));
    const gap = 8;
    const viewport = nativeViewportEl?.getBoundingClientRect();
    const leftBound = (viewport?.left ?? sb.left) + gap;
    const rightBound = (viewport?.right ?? sb.right) - gap;
    const topBound = (viewport?.top ?? 0) + gap;
    const bottomBound = (viewport?.bottom ?? sb.top) - gap;
    const centerX = sb.left + clamp(lastPreviewRatio, 0, 1) * sb.width;
    const x = clamp(centerX - width / 2, leftBound, Math.max(leftBound, rightBound - width));
    const y = clamp(sb.top - height - gap, topBound, Math.max(topBound, bottomBound - height));
    return { x, y, width, height };
}

async function blobToBase64(blob: Blob): Promise<string | null> {
    try {
        const bytes = new Uint8Array(await blob.arrayBuffer());
        let binary = "";
        const chunk = 0x8000;
        for (let i = 0; i < bytes.length; i += chunk) {
            binary += String.fromCharCode(...bytes.subarray(i, i + chunk));
        }
        return btoa(binary);
    } catch {
        return null;
    }
}

async function objectURLToBase64(url: string): Promise<string | null> {
    try {
        const response = await fetch(url);
        return await blobToBase64(await response.blob());
    } catch {
        return null;
    }
}

function thumbnailBucket(seconds: number) {
    if (!Number.isFinite(seconds) || seconds < 0) return 0;
    const interval = thumbnailBucketInterval(currentState.duration);
    return Math.max(0, Math.round(seconds / interval) * interval);
}

function thumbnailBucketInterval(duration: number) {
    if (duration >= 2 * 60 * 60) return THUMBNAIL_VERY_LONG_BUCKET_SECONDS;
    if (duration >= 30 * 60) return THUMBNAIL_LONG_BUCKET_SECONDS;
    return THUMBNAIL_BUCKET_SECONDS;
}

function clearThumbnailRequestTimer() {
    if (thumbnailRequestTimer == null) return;
    window.clearTimeout(thumbnailRequestTimer);
    thumbnailRequestTimer = null;
    scheduledThumbnailBucket = -1;
}

function clearThumbnailDwellTimer() {
    if (thumbnailDwellTimer == null) return;
    window.clearTimeout(thumbnailDwellTimer);
    thumbnailDwellTimer = null;
}

function scheduleThumbnailRequest(bucket: number, keepVisible = false) {
    if (!activeThumbnailURL || thumbnailObjectURLs.has(bucket)) return;
    scheduledThumbnailBucket = bucket;
    if (thumbnailRequestTimer != null) window.clearTimeout(thumbnailRequestTimer);
    thumbnailRequestTimer = window.setTimeout(() => {
        thumbnailRequestTimer = null;
        const bucketToRequest = scheduledThumbnailBucket;
        scheduledThumbnailBucket = -1;
        if (bucketToRequest !== currentPreviewBucket) return;
        requestThumbnail(bucketToRequest, false, keepVisible);
    }, THUMBNAIL_REQUEST_DEBOUNCE_MS);
}

function scheduleThumbnailDwell(bucket: number) {
    clearThumbnailDwellTimer();
    if (!activeThumbnailURL || !thumbnailObjectURLs.has(bucket) || seekingWithPointer) return;
    thumbnailDwellTimer = window.setTimeout(() => {
        thumbnailDwellTimer = null;
        if (currentPreviewBucket !== bucket || seekingWithPointer || !thumbnailObjectURLs.has(bucket)) return;
        const interval = thumbnailBucketInterval(currentState.duration);
        for (const neighbor of [bucket - interval, bucket + interval, bucket - 2 * interval, bucket + 2 * interval]) {
            if (neighbor < 0 || neighbor > currentState.duration) continue;
            requestThumbnail(neighbor, true);
        }
    }, THUMBNAIL_DWELL_PREFETCH_MS);
}

function requestThumbnail(bucket: number, prefetch = false, keepVisible = false) {
    if (!activeThumbnailURL || pendingThumbnails.has(bucket) || thumbnailObjectURLs.has(bucket)) return;
    if (thumbnailFailedRecently(bucket)) return;
    pendingThumbnails.add(bucket);
    if (!prefetch && !keepVisible && currentPreviewBucket === bucket) setThumbnailTooltipState("pending");
    const seq = thumbnailRequestSeq;
    const url = `${activeThumbnailURL}?t=${encodeURIComponent(String(bucket))}`;
    let retryScheduled = false;
    fetch(url, { cache: "no-store" })
        .then(async (response) => {
            if (seq !== thumbnailRequestSeq) return;
            if (response.status === 202) {
                retryScheduled = true;
                window.setTimeout(() => {
                    pendingThumbnails.delete(bucket);
                    if (seq === thumbnailRequestSeq && currentPreviewBucket === bucket) requestThumbnail(bucket);
                }, THUMBNAIL_RETRY_MS);
                return;
            }
            if (!response.ok) {
                failedThumbnails.set(bucket, Date.now());
                if (!prefetch && !keepVisible && currentPreviewBucket === bucket) setThumbnailTooltipState("failed");
                return;
            }
            const blob = await response.blob();
            if (!blob.size || seq !== thumbnailRequestSeq) return;
            const nativeImage = isNativeFallbackActive() ? await blobToBase64(blob) : null;
            if (seq !== thumbnailRequestSeq) return;
            const objectURL = URL.createObjectURL(blob);
            const old = thumbnailObjectURLs.get(bucket);
            if (old) URL.revokeObjectURL(old);
            thumbnailObjectURLs.set(bucket, objectURL);
            if (nativeImage) thumbnailBase64.set(bucket, nativeImage);
            failedThumbnails.delete(bucket);
            if (currentPreviewBucket === bucket && scrubberTooltipImageEl && scrubberTooltipEl) {
                scrubberTooltipImageEl.src = objectURL;
                setThumbnailTooltipState("ready");
                positionScrubberTooltip(lastPreviewRatio);
                scheduleThumbnailDwell(bucket);
                presentNativeSeekPreview(bucket);
            }
        })
        .catch(() => {
            if (seq === thumbnailRequestSeq) failedThumbnails.set(bucket, Date.now());
            if (!prefetch && !keepVisible && seq === thumbnailRequestSeq && currentPreviewBucket === bucket) setThumbnailTooltipState("failed");
        })
        .finally(() => {
            if (seq === thumbnailRequestSeq && !retryScheduled) pendingThumbnails.delete(bucket);
        });
}

function thumbnailFailedRecently(bucket: number) {
    const failedAt = failedThumbnails.get(bucket);
    return Boolean(failedAt && Date.now() - failedAt < THUMBNAIL_FAILURE_TTL_MS);
}

function setThumbnailTooltipState(state: "pending" | "ready" | "failed") {
    if (!scrubberTooltipEl) return;
    scrubberTooltipEl.classList.toggle("has-thumbnail", state === "ready");
    scrubberTooltipEl.classList.toggle("is-thumbnail-pending", state === "pending");
    scrubberTooltipEl.classList.toggle("is-thumbnail-failed", state === "failed");
    if (state !== "ready") scrubberTooltipImageEl?.removeAttribute("src");
}

function resetThumbnailPreview() {
    thumbnailRequestSeq += 1;
    activeThumbnailURL = "";
    currentPreviewBucket = -1;
    lastPreviewRatio = 0;
    clearThumbnailRequestTimer();
    clearThumbnailDwellTimer();
    pendingThumbnails.clear();
    failedThumbnails.clear();
    for (const objectURL of thumbnailObjectURLs.values()) {
        URL.revokeObjectURL(objectURL);
    }
    thumbnailObjectURLs.clear();
    thumbnailBase64.clear();
    nativeSeekAspect = 9 / 16;
    hideNativeSeekPreview();
    if (scrubberTooltipImageEl) scrubberTooltipImageEl.removeAttribute("src");
    if (scrubberTooltipTimeEl) scrubberTooltipTimeEl.textContent = "0:00";
    scrubberTooltipEl?.classList.remove("has-thumbnail", "is-thumbnail-pending", "is-thumbnail-failed");
}

function updateScrubVisual(seconds: number) {
    const played = percent(seconds, currentState.duration);
    if (scrubberPlayedEl) scrubberPlayedEl.style.width = `${played}%`;
    if (scrubberThumbEl) scrubberThumbEl.style.left = `${played}%`;
    if (timeEl) timeEl.textContent = formatTime(seconds);
}

function volumeFromEvent(event: PointerEvent | MouseEvent) {
    if (!volumeSliderEl) return currentState.volume;
    const rect = volumeSliderEl.getBoundingClientRect();
    return clamp((event.clientX - rect.left) / Math.max(1, rect.width), 0, 1);
}

function setVolumeFromPointer(event: PointerEvent | MouseEvent) {
    const value = volumeFromEvent(event);
    previewVolume(value);
    scheduleVolumeSet(value);
    revealChrome();
}

function scheduleVolumeSet(value: number) {
    pendingVolumeValue = clamp(value, 0, 1);
    if (volumeCommandFrame) return;
    volumeCommandFrame = requestAnimationFrame(() => {
        volumeCommandFrame = 0;
        const next = pendingVolumeValue;
        pendingVolumeValue = null;
        if (next == null) return;
        activeAdapter?.setVolume(next);
    });
}

function clearVolumeCommandFrame() {
    if (volumeCommandFrame) {
        cancelAnimationFrame(volumeCommandFrame);
        volumeCommandFrame = 0;
    }
    pendingVolumeValue = null;
}

async function releaseActive() {
    const adapter = activeAdapter;
    activeAdapter = null;
    activeNative = null;
    activeMediaToken = "";
    unsubscribeState?.();
    unsubscribeState = null;
    clearPlaybackHintTimer();
    clearMediaStatsPolling();
    clearVolumeCommandFrame();
    clearSkipFeedback();
    resetThumbnailPreview();
    setSpeedMenuOpen(false);
    setNativeMode(false);
    if (adapter) {
        try {
            await adapter.close();
        } catch (err) {
            console.warn("Close media failed:", err);
        }
    }
    currentState = { ...EMPTY_STATE };
    applyState(currentState);
}

function releaseActiveSoon() {
    const adapter = activeAdapter;
    activeAdapter = null;
    activeNative = null;
    activeMediaToken = "";
    unsubscribeState?.();
    unsubscribeState = null;
    clearPlaybackHintTimer();
    clearMediaStatsPolling();
    clearVolumeCommandFrame();
    clearSkipFeedback();
    resetThumbnailPreview();
    setSpeedMenuOpen(false);
    setNativeMode(false);
    if (adapter) {
        void adapter.close().catch((err) => {
            console.warn("Close media failed:", err);
        });
    }
    currentState = { ...EMPTY_STATE };
    applyState(currentState);
}

function updateMediaText(name: string, size: number) {
    if (filenameEl) filenameEl.textContent = name || "Video";
    mediaMetaBaseText = `${videoFormatLabel(name)}${size ? ` · ${formatBytes(size)}` : ""}`;
    mediaMetaBytes = size || 0;
    renderMediaMeta();
}

function renderMediaMeta() {
    if (!metaEl) return;
    metaEl.textContent = streamActivityText ? `${mediaMetaBaseText} · ${streamActivityText}` : mediaMetaBaseText;
}

export async function openVideoModal(target: VideoOpenTarget) {
    if (!modalEl || !videoEl || !filenameEl || !metaEl) return;
    const id = Number(target.id || 0);
    if (!id) return;

    const seq = ++openSeq;
    releaseActiveSoon();

    updateMediaText(target.name || "Video", target.size || 0);
    clearError();
    setLoading(true);
    setChromeVisible(true);
    modalEl.style.display = "flex";
    modalEl.setAttribute("aria-hidden", "false");
    a11y?.activate();
    void syncFullscreenState();

    if (target.encrypted) {
        setError("Encrypted videos can't be played yet.");
        return;
    }
    if (!isWebviewDirectVideo(target.name)) {
        const measureFallback = shouldMeasureNativeFallbackBeforeOpen();
        // macOS stays opaque until libmpv exists underneath the transparent stage.
        // Windows/Linux must enter fallback layout first so the native child HWND/X11
        // window is never created over the close button or transport controls.
        setNativeMode(measureFallback, measureFallback);
        await nextFrame();
        const rect = currentNativeRect();
        if (seq !== openSeq || !isOpen()) return;
        if (!rect) {
            setNativeMode(false);
            setError("Could not prepare the native video surface.");
            return;
        }
        try {
            const opened = await openNativeMedia(id, rect);
            if (seq !== openSeq || !isOpen()) {
                await closeNativeMedia(opened.token);
                return;
            }
            if (!opened.token) {
                throw new Error("native media session did not return a token");
            }
            setNativeMode(true, !opened.htmlControls);
            activeNative = opened;
            activeMediaToken = opened.token;
            const displayName = opened.info.name || opened.name || target.name || "Video";
            const displaySize = opened.info.plaintextSize || opened.info.storedSize || target.size || 0;
            updateMediaText(displayName, displaySize);
            const adapter = new NativeMpvAdapter(opened);
            activeAdapter = adapter;
            activeThumbnailURL = opened.thumbnailUrl;
            thumbnailRequestSeq += 1;
            unsubscribeState = adapter.subscribe(applyState);
            setChromeVisible(true);
            scheduleNativeResize();
        } catch (err: unknown) {
            console.error("OpenNativeMedia failed:", err);
            setNativeMode(false);
            setError(errorMessage(err, "Could not open this video."));
        }
        return;
    }
    setNativeMode(false);

    try {
        const opened = await openMedia(id);
        if (seq !== openSeq || !isOpen()) {
            await closeMedia(opened.token);
            return;
        }
        if (!opened.url || !opened.token) {
            throw new Error("media session did not return a playable URL");
        }
        const displayName = opened.info.name || opened.name || target.name || "Video";
        const displaySize = opened.info.plaintextSize || opened.info.storedSize || target.size || 0;
        updateMediaText(displayName, displaySize);
        activeThumbnailURL = opened.thumbnailUrl;
        activeMediaToken = opened.token;
        thumbnailRequestSeq += 1;
        const adapter = new HtmlVideoAdapter(videoEl, opened);
        activeAdapter = adapter;
        unsubscribeState = adapter.subscribe(applyState);
		adapter.load();
    } catch (err: unknown) {
        console.error("OpenMedia failed:", err);
        setError(errorMessage(err, "Could not open this video."));
    }
}

export async function closeVideoModal() {
    if (!modalEl) return;
    openSeq += 1;
    clearChromeTimer();
    await exitVideoFullscreen();
    modalEl.style.display = "none";
    modalEl.setAttribute("aria-hidden", "true");
    a11y?.deactivate();
    await releaseActive();
    clearError();
    setLoading(false);
}

function togglePlayback() {
    if (!activeAdapter || hasError) return;
    activeAdapter.playPause();
    revealChrome();
}

function seekBy(delta: number) {
    if (!activeAdapter || (currentState.duration <= 0 && !activeNative)) return;
    activeAdapter.seekRelative(delta);
    showSkipFeedback(delta);
    revealChrome();
}

function bindVideoEvents() {
    if (!videoEl) return;
    videoEl.addEventListener("error", () => {
        const code = videoEl?.error?.code;
        const hint = code ? ` (media error ${code})` : "";
        setError(`This format could not be decoded by the embedded webview${hint}.`);
        void releaseActive();
    });
}

function bindScrubber() {
    scrubberEl?.addEventListener("pointerenter", (event) => {
        scrubberEl?.classList.add("is-hovered");
        previewScrubber(event);
    });
    scrubberEl?.addEventListener("pointermove", (event) => {
        previewScrubber(event);
        if (!seekingWithPointer) return;
        updateScrubVisual(scrubberSecondsFromEvent(event));
    });
    scrubberEl?.addEventListener("pointerleave", () => {
        currentPreviewBucket = -1;
        clearThumbnailDwellTimer();
        hideNativeSeekPreview();
        if (!seekingWithPointer) scrubberEl?.classList.remove("is-hovered");
        scheduleChromeHide();
    });
    scrubberEl?.addEventListener("pointerdown", (event) => {
        if (!activeAdapter || currentState.duration <= 0) return;
        seekingWithPointer = true;
        scrubberEl?.setPointerCapture(event.pointerId);
        scrubberEl?.classList.add("is-dragging", "is-hovered");
        updateScrubVisual(scrubberSecondsFromEvent(event));
        revealChrome();
    });
    scrubberEl?.addEventListener("pointerup", (event) => {
        if (!activeAdapter || currentState.duration <= 0) return;
        const seconds = scrubberSecondsFromEvent(event);
        seekingWithPointer = false;
        if (scrubberEl?.hasPointerCapture(event.pointerId)) {
            scrubberEl.releasePointerCapture(event.pointerId);
        }
        scrubberEl?.classList.remove("is-dragging");
        activeAdapter.seekAbsolute(seconds);
        revealChrome();
    });
    scrubberEl?.addEventListener("pointercancel", (event) => {
        seekingWithPointer = false;
        if (scrubberEl?.hasPointerCapture(event.pointerId)) {
            scrubberEl.releasePointerCapture(event.pointerId);
        }
        scrubberEl?.classList.remove("is-dragging", "is-hovered");
        syncTimeline(currentState);
    });
    scrubberEl?.addEventListener("keydown", (event) => {
        const handled = ["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key);
        if (!handled) return;
        event.preventDefault();
        event.stopPropagation();
        if (!activeAdapter || currentState.duration <= 0) return;
        if (event.key === "ArrowLeft") {
            activeAdapter.seekRelative(-SEEK_STEP_SECONDS);
        } else if (event.key === "ArrowRight") {
            activeAdapter.seekRelative(SEEK_STEP_SECONDS);
        } else if (event.key === "Home") {
            activeAdapter.seekAbsolute(0);
        } else if (event.key === "End") {
            activeAdapter.seekAbsolute(currentState.duration);
        }
    });
}

function bindVolume() {
    muteBtnEl?.addEventListener("click", () => {
        activeAdapter?.setMuted(!currentState.muted);
        revealChrome();
    });
    volumeSliderEl?.addEventListener("pointerdown", (event) => {
        if (!activeAdapter) return;
        volumeDragging = true;
        volumeSliderEl?.setPointerCapture(event.pointerId);
        setVolumeFromPointer(event);
    });
    volumeSliderEl?.addEventListener("pointermove", (event) => {
        if (!volumeDragging) return;
        setVolumeFromPointer(event);
    });
    volumeSliderEl?.addEventListener("pointerup", (event) => {
        if (!volumeDragging) return;
        volumeDragging = false;
        if (volumeSliderEl?.hasPointerCapture(event.pointerId)) {
            volumeSliderEl.releasePointerCapture(event.pointerId);
        }
        setVolumeFromPointer(event);
    });
    volumeSliderEl?.addEventListener("pointercancel", (event) => {
        volumeDragging = false;
        if (volumeSliderEl?.hasPointerCapture(event.pointerId)) {
            volumeSliderEl.releasePointerCapture(event.pointerId);
        }
    });
    volumeSliderEl?.addEventListener("keydown", (event) => {
        const handled = ["ArrowLeft", "ArrowDown", "ArrowRight", "ArrowUp", "Home", "End"].includes(event.key);
        if (!handled) return;
        event.preventDefault();
        event.stopPropagation();
        if (!activeAdapter) return;
        if (event.key === "ArrowLeft" || event.key === "ArrowDown") {
            activeAdapter.setVolume(currentState.volume - VOLUME_STEP);
        } else if (event.key === "ArrowRight" || event.key === "ArrowUp") {
            activeAdapter.setVolume(currentState.volume + VOLUME_STEP);
        } else if (event.key === "Home") {
            activeAdapter.setVolume(0);
        } else if (event.key === "End") {
            activeAdapter.setVolume(1);
        }
    });
}

function isSpeedMenuOpen() {
    return Boolean(speedMenuEl?.classList.contains("is-open"));
}

function speedMenuButtons() {
    return Array.from(speedMenuEl?.querySelectorAll<HTMLButtonElement>("[data-rate]") || []);
}

function selectedSpeedButton() {
    return speedMenuButtons().find((button) => button.classList.contains("is-selected")) || speedMenuButtons()[0] || null;
}

function setSpeedMenuOpen(open: boolean) {
    if (open && isNativeFallbackActive()) open = false;
    speedBtnEl?.setAttribute("aria-expanded", open ? "true" : "false");
    speedMenuEl?.classList.toggle("is-open", open);
    if (open) {
        clearChromeTimer();
        requestAnimationFrame(() => selectedSpeedButton()?.focus({ preventScroll: true }));
    } else if (isOpen() && !currentState.paused && !hasError) {
        scheduleChromeHide();
    }
}

function closeSpeedMenu(restoreFocus = false) {
    if (!isSpeedMenuOpen()) return;
    setSpeedMenuOpen(false);
    if (restoreFocus) speedBtnEl?.focus({ preventScroll: true });
}

function moveSpeedMenuFocus(delta: number) {
    const buttons = speedMenuButtons();
    if (buttons.length === 0) return;
    const active = document.activeElement as HTMLButtonElement | null;
    const current = Math.max(0, buttons.indexOf(active as HTMLButtonElement));
    buttons[(current + delta + buttons.length) % buttons.length].focus({ preventScroll: true });
}

function bindSpeedMenu() {
    speedBtnEl?.addEventListener("click", (event) => {
        event.stopPropagation();
        if (isNativeFallbackActive()) {
            cycleFallbackPlaybackRate();
            return;
        }
        setSpeedMenuOpen(!isSpeedMenuOpen());
        revealChrome();
    });
    speedMenuEl?.addEventListener("click", (event) => {
        const button = (event.target as HTMLElement | null)?.closest<HTMLButtonElement>("[data-rate]");
        if (!button || !activeAdapter) return;
        activeAdapter.setSpeed(Number(button.dataset.rate || 1));
        closeSpeedMenu(true);
        revealChrome();
    });
    speedMenuEl?.addEventListener("keydown", (event) => {
        if (!isSpeedMenuOpen()) return;
        if (event.key === "Escape") {
            event.preventDefault();
            event.stopPropagation();
            closeSpeedMenu(true);
        } else if (event.key === "ArrowDown" || event.key === "ArrowRight") {
            event.preventDefault();
            event.stopPropagation();
            moveSpeedMenuFocus(1);
        } else if (event.key === "ArrowUp" || event.key === "ArrowLeft") {
            event.preventDefault();
            event.stopPropagation();
            moveSpeedMenuFocus(-1);
        } else if (event.key === "Home") {
            event.preventDefault();
            event.stopPropagation();
            speedMenuButtons()[0]?.focus({ preventScroll: true });
        } else if (event.key === "End") {
            event.preventDefault();
            event.stopPropagation();
            const buttons = speedMenuButtons();
            buttons[buttons.length - 1]?.focus({ preventScroll: true });
        } else if (event.key === "Enter" || event.key === " ") {
            event.preventDefault();
            event.stopPropagation();
            (document.activeElement as HTMLButtonElement | null)?.click();
        }
    });
    document.addEventListener("click", (event) => {
        if (!isSpeedMenuOpen()) return;
        const target = event.target as Node | null;
        if (target && (speedMenuEl?.contains(target) || speedBtnEl?.contains(target))) return;
        closeSpeedMenu();
    });
}

function targetShouldUseOwnKeyboard(target: HTMLElement | null, event: KeyboardEvent) {
    if (!target) return false;
    if (target.isContentEditable) return true;
    const tag = String(target.tagName || "").toUpperCase();
    if (tag === "BUTTON") {
        if (target.closest("#video-speed-menu")) return true;
        if (target.closest("#video-modal")) {
            return event.key === "Enter" || event.code === "Space" || event.key === " ";
        }
        return true;
    }
    if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") return true;
    if (target.closest("#video-speed-menu")) return true;
    if (target.closest("#video-scrubber")) {
        return ["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key);
    }
    if (target.closest("#video-volume-slider")) {
        return ["ArrowLeft", "ArrowDown", "ArrowRight", "ArrowUp", "Home", "End"].includes(event.key);
    }
    return false;
}

function handleVideoShortcut(event: KeyboardEvent) {
    if (!isOpen()) return;
    const target = event.target as HTMLElement | null;
    if (targetShouldUseOwnKeyboard(target, event)) return;
    const key = event.key.toLowerCase();
    if (event.code === "Space" || event.key === " " || key === "k") {
        event.preventDefault();
        togglePlayback();
    } else if (event.key === "ArrowLeft" || key === "j") {
        event.preventDefault();
        seekBy(-SEEK_STEP_SECONDS);
    } else if (event.key === "ArrowRight" || key === "l") {
        event.preventDefault();
        seekBy(SEEK_STEP_SECONDS);
    } else if (event.key === "ArrowUp") {
        event.preventDefault();
        activeAdapter?.setVolume(currentState.volume + VOLUME_STEP);
        revealChrome();
    } else if (event.key === "ArrowDown") {
        event.preventDefault();
        activeAdapter?.setVolume(currentState.volume - VOLUME_STEP);
        revealChrome();
    } else if (key === "m") {
        event.preventDefault();
        activeAdapter?.setMuted(!currentState.muted);
        revealChrome();
    } else if (key === "f") {
        event.preventDefault();
        void toggleFullscreen();
    }
}

function handleVideoPointerMove() {
    revealChrome();
}

function targetIsVideoChrome(target: EventTarget | null) {
    const el = target as HTMLElement | null;
    return Boolean(el?.closest(".video-topbar, .video-controls, .video-center-controls, .video-error, .video-loading"));
}

function handleStageClick(event: MouseEvent) {
    if (targetIsVideoChrome(event.target)) return;
    togglePlayback();
}

function handleWindowResize() {
    scheduleNativeResize();
    void syncFullscreenState();
}

function bindControls() {
    closeBtnEl?.addEventListener("click", () => void closeVideoModal());
    centerPlayBtnEl?.addEventListener("click", togglePlayback);
    centerSkipBackBtnEl?.addEventListener("click", () => seekBy(-SEEK_STEP_SECONDS));
    centerSkipForwardBtnEl?.addEventListener("click", () => seekBy(SEEK_STEP_SECONDS));
    skipBackBtnEl?.addEventListener("click", () => seekBy(-SEEK_STEP_SECONDS));
    skipForwardBtnEl?.addEventListener("click", () => seekBy(SEEK_STEP_SECONDS));
    playBtnEl?.addEventListener("click", togglePlayback);
    fullscreenBtnEl?.addEventListener("click", () => void toggleFullscreen());
    bindScrubber();
    bindVolume();
    bindSpeedMenu();
    modalEl?.addEventListener("pointermove", handleVideoPointerMove);
    stageEl?.addEventListener("click", handleStageClick);
    document.addEventListener("keydown", handleVideoShortcut);
    window.addEventListener("resize", handleWindowResize);
}

function renderSpeedOptions() {
    if (!speedMenuEl) return;
    speedMenuEl.innerHTML = RATE_OPTIONS.map((rate) => (
        `<button type="button" role="menuitemradio" data-rate="${rate}" aria-checked="${rate === 1 ? "true" : "false"}"><span class="video-speed-check" aria-hidden="true">✓</span><span>${formatRate(rate)}x</span></button>`
    )).join("");
}

export function setupVideoModal() {
    modalEl = byID("video-modal");
    stageEl = byID("video-stage");
    topbarEl = document.querySelector<HTMLElement>("#video-modal .video-topbar");
    controlsEl = document.querySelector<HTMLElement>("#video-modal .video-controls");
    filenameEl = byID("video-filename");
    metaEl = byID("video-meta");
    closeBtnEl = byID("video-close");
    nativeViewportEl = byID("video-native-viewport");
    videoEl = byID("video-player");
    loadingEl = byID("video-loading");
    loadingStatusEl = byID("video-loading-status");
    errorEl = byID("video-error");
    centerControlsEl = byID("video-center-controls");
    centerPlayBtnEl = byID("video-center-play");
    centerSkipBackBtnEl = byID("video-center-skip-back");
    centerSkipForwardBtnEl = byID("video-center-skip-forward");
    skipFeedbackEl = byID("video-skip-feedback");
    playBtnEl = byID("video-play");
    skipBackBtnEl = byID("video-skip-back");
    skipForwardBtnEl = byID("video-skip-forward");
    muteBtnEl = byID("video-mute");
    fullscreenBtnEl = byID("video-fullscreen");
    scrubberEl = byID("video-scrubber");
    scrubberPlayedEl = byID("video-scrubber-played");
    scrubberBufferedEl = byID("video-scrubber-buffered");
    scrubberThumbEl = byID("video-scrubber-thumb");
    scrubberTooltipEl = byID("video-scrubber-tooltip");
    scrubberTooltipImageEl = byID("video-scrubber-tooltip-image");
    scrubberTooltipTimeEl = byID("video-scrubber-tooltip-time");
	volumeSliderEl = byID("video-volume-slider");
	volumeFillEl = byID("video-volume-fill");
    volumeThumbEl = byID("video-volume-thumb");
    timeEl = byID("video-time");
    durationEl = byID("video-duration");
    speedBtnEl = byID("video-speed-button");
    speedMenuEl = byID("video-speed-menu");

    if (!modalEl || !videoEl || !stageEl) {
        console.error("Video modal setup failed. Missing #video-modal, #video-stage, or #video-player.");
        return;
    }
    a11y = installModalA11y(modalEl, {
        requestClose: () => {
            if (isSpeedMenuOpen()) {
                closeSpeedMenu(true);
                return;
            }
            void closeVideoModal();
        },
        initialFocus: () => playBtnEl || closeBtnEl,
        restoreFocus: "#file-list",
    });
    renderSpeedOptions();
    bindVideoEvents();
    bindControls();
    applyState(EMPTY_STATE);
}
