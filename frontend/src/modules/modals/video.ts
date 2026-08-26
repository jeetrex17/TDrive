import {
    attachNativeMedia,
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
    type NativeMediaStatePayload,
} from "../../api";
import { EventsOn, WindowFullscreen, WindowIsFullscreen, WindowUnfullscreen } from "../../../wailsjs/runtime/runtime";
import { formatBytes } from "../../utils";
import { isWebviewDirectVideo, videoFormatLabel } from "../media-types";
import {
    SerialPlaybackTransitions,
    capturePlaybackIntent,
    shouldFallbackFromHtmlMediaError,
    type PlaybackIntent,
} from "../video/playback-lifecycle";
import {
    nativeTrackLabel,
    normalizeNativeTracks,
    type NativeMediaTrack,
} from "../video/media-tracks";
import { installModalA11y } from "../../ui/modals/modal-a11y";
import VideoModal from "../../ui/video/VideoModal.svelte";
import { mountSvelte, type SvelteMountHandle } from "../../ui/mount";

const CHROME_HIDE_DELAY_MS = 2500;
const LOADING_DEBOUNCE_MS = 250;
const SEEK_STEP_SECONDS = 10;
const VOLUME_STEP = 0.05;
const RATE_OPTIONS = [0.5, 0.75, 1, 1.25, 1.5, 2];
const MIN_PLAYBACK_RATE = 0.25;
const MAX_PLAYBACK_RATE = 4;
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
    tracks: NativeMediaTrack[];
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

interface VideoOpenAttempt {
    generation: number;
    target: VideoOpenTarget;
    htmlFailureHandled: boolean;
    nativeFallbackRequested: boolean;
    pausedByUser: boolean;
}

type HtmlMediaErrorHandler = (code: number | undefined, state: PlayerState) => void;
type NativeMediaErrorHandler = (detail: string) => void;
type NativeMediaClosedHandler = () => void;
type NativeLayout = "none" | "embedded-overlay" | "embedded-fallback" | "standalone";

const EMPTY_STATE: PlayerState = {
    paused: true,
    currentTime: 0,
    duration: 0,
    buffered: [],
    volume: 1,
    muted: false,
    rate: 1,
    loading: false,
    tracks: [],
};

class HtmlVideoAdapter implements PlayerAdapter {
    private subscribers = new Set<(state: PlayerState) => void>();
    private listeners: Array<() => void> = [];
    private closed = false;
    private lastAudibleVolume: number;

    constructor(
        private readonly video: HTMLVideoElement,
        private readonly opened: MediaOpenResult,
        private readonly onMediaError: HtmlMediaErrorHandler,
    ) {
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
        const errorListener = () => {
            if (this.closed) return;
            this.onMediaError(this.video.error?.code, this.snapshot());
        };
        video.addEventListener("error", errorListener);
        this.listeners.push(() => video.removeEventListener("error", errorListener));
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
        this.video.playbackRate = clampPlaybackRate(value);
        this.emit();
    }

    async close() {
        if (this.closed) return;
        try {
            this.detach();
        } finally {
            await closeMedia(this.opened.token);
        }
    }

    detachForNative(): MediaOpenResult | null {
        return this.detach() ? this.opened : null;
    }

    private detach(): boolean {
        if (this.closed) return false;
        this.closed = true;
        for (const remove of this.listeners.splice(0)) remove();
        this.subscribers.clear();
        try {
            this.video.pause();
        } catch {
            // The token still has to be released if a platform media element is
            // already torn down and rejects a final pause/reset.
        }
        this.video.removeAttribute("src");
        try {
            this.video.load();
        } catch {
            // Removing src is sufficient to detach ownership for native handoff.
        }
        return true;
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
            tracks: [],
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
    private commandFlushFrame = 0;
    private pendingSeek: { mode: "absolute" | "relative"; value: number } | null = null;
    private pendingLatestCommands = new Map<string, string[]>();
    private lastAudibleVolume = 1;
    private failureReported = false;
    private closeReported = false;
    private lastSequence = 0;

    constructor(
        private readonly opened: NativeMediaOpenResult,
        private readonly onMediaError: NativeMediaErrorHandler,
        private readonly onMediaClosed: NativeMediaClosedHandler,
    ) {
        this.state = { ...EMPTY_STATE, paused: false, loading: true };
    }

    start(pending: NativeMediaStatePayload | null) {
        if (this.opened.initialState) {
            this.applyPayload(this.opened.initialState);
        }
        if (pending) this.applyPayload(pending);
    }

    accepts(payload: NativeMediaStatePayload) {
        return !this.closed && payload.token === this.opened.token;
    }

    receive(payload: NativeMediaStatePayload) {
        if (this.accepts(payload)) this.applyPayload(payload);
    }

    isTerminal() {
        return this.failureReported || this.closeReported;
    }

    private applyPayload(payload: NativeMediaStatePayload) {
        const sequence = Number(payload.sequence ?? 0);
        if (sequence > 0) {
            if (sequence <= this.lastSequence) return;
            this.lastSequence = sequence;
        }
        const failure = nativeFailureDetail(payload);
        if (failure) {
            if (!this.failureReported) {
                this.failureReported = true;
                this.onMediaError(failure);
            }
            return;
        }
        if (payload.status === "closed") {
            if (!this.closeReported) {
                this.closeReported = true;
                this.onMediaClosed();
            }
            return;
        }
        this.state = nativePayloadToState(payload, this.state);
        if (!this.state.muted && this.state.volume > 0) this.lastAudibleVolume = this.state.volume;
        this.emit();
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
        const next = clampPlaybackRate(value);
        this.scheduleLatestCommand("speed", ["set", "speed", String(next)]);
        this.updateFallbackState((state) => ({ ...state, rate: next }));
    }

    setAudioTrack(id: number) {
        if (!Number.isSafeInteger(id) || id <= 0) return;
        void this.sendCommand(["set", "aid", String(id)]);
    }

    setSubtitleTrack(id: number | null) {
        if (id !== null && (!Number.isSafeInteger(id) || id <= 0)) return;
        void this.sendCommand(["set", "sid", id === null ? "no" : String(id)]);
    }

    async close() {
        if (this.closed) return;
        this.closed = true;
        this.clearScheduledCommands();
        if (activeNativeStateAdapter === this) activeNativeStateAdapter = null;
        this.subscribers.clear();
        try {
            await closeNativeMedia(this.opened.token);
        } finally {
            pendingNativeMediaStates.delete(this.opened.token);
        }
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
let standaloneEl: HTMLElement | null = null;
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
let trackControlsEl: HTMLElement | null = null;
let audioControlEl: HTMLElement | null = null;
let audioSelectEl: HTMLSelectElement | null = null;
let subtitleControlEl: HTMLElement | null = null;
let subtitleSelectEl: HTMLSelectElement | null = null;

let activeAdapter: PlayerAdapter | null = null;
let activeNativeStateAdapter: NativeMpvAdapter | null = null;
let activeNative: NativeMediaOpenResult | null = null;
let activeMediaToken = "";
let activeMediaEncrypted = false;
let unsubscribeState: (() => void) | null = null;
let currentState: PlayerState = { ...EMPTY_STATE };
let activeOpenAttempt: VideoOpenAttempt | null = null;
const playbackTransitions = new SerialPlaybackTransitions();
let unsubscribeEncryptedMediaSessionsClosed: (() => void) | null = null;
let unsubscribeNativeMediaState: (() => void) | null = null;
const pendingNativeMediaStates = new Map<string, NativeMediaStatePayload>();
let chromeHideTimer: ReturnType<typeof setTimeout> | null = null;
let loadingTimer: ReturnType<typeof setTimeout> | null = null;
let loadingStatusOverride = "";
let skipFeedbackTimer: ReturnType<typeof setTimeout> | null = null;
let nativeResizeFrame = 0;
let seekingWithPointer = false;
let volumeDragging = false;
let pendingVolumeValue: number | null = null;
let volumeCommandFrame = 0;
let hasError = false;
let isWindowFullscreen = false;
let lastBufferedSignature = "";
let lastTrackSignature = "";
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
let videoMarkupHandle: SvelteMountHandle<Record<string, unknown>> | null = null;
let videoSetupComplete = false;

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
        tracks: Array.isArray(payload.tracks) ? normalizeNativeTracks(payload.tracks) : previous.tracks,
    };
}

function nativeFailureDetail(payload: NativeMediaStatePayload): string | null {
    const detail = typeof payload.error === "string" ? payload.error.trim().slice(0, 256) : "";
    if (payload.status !== "failed" && !detail) return null;
    return detail || "native media player exited unexpectedly";
}

function normalizeNativeMediaStatePayload(value: unknown): NativeMediaStatePayload | null {
    if (!value || typeof value !== "object") return null;
    const raw = value as NativeMediaStatePayload;
    const token = typeof raw.token === "string" ? raw.token : "";
    if (!token || token.length > 512 || token.trim() !== token) return null;
    const sequence = Number(raw.sequence ?? 0);
    return {
        ...raw,
        token,
        sequence: Number.isSafeInteger(sequence) && sequence > 0 ? sequence : 0,
    };
}

function nativeStateSequence(payload: NativeMediaStatePayload | null | undefined) {
    const sequence = Number(payload?.sequence ?? 0);
    return Number.isSafeInteger(sequence) && sequence > 0 ? sequence : 0;
}

function cachePendingNativeMediaState(payload: NativeMediaStatePayload) {
    const token = payload.token!;
    const previous = pendingNativeMediaStates.get(token);
    const sequence = nativeStateSequence(payload);
    const previousSequence = nativeStateSequence(previous);
    if (previous && previousSequence > 0 && (sequence === 0 || sequence <= previousSequence)) return;
    pendingNativeMediaStates.delete(token);
    pendingNativeMediaStates.set(token, payload);
    while (pendingNativeMediaStates.size > 16) {
        const oldest = pendingNativeMediaStates.keys().next().value;
        if (typeof oldest !== "string") break;
        pendingNativeMediaStates.delete(oldest);
    }
}

function routeNativeMediaState(value: unknown) {
    const payload = normalizeNativeMediaStatePayload(value);
    if (!payload) return;
    if (activeNativeStateAdapter?.accepts(payload)) {
        activeNativeStateAdapter.receive(payload);
        return;
    }
    cachePendingNativeMediaState(payload);
}

function takePendingNativeMediaState(token: string) {
    const payload = pendingNativeMediaStates.get(token) ?? null;
    pendingNativeMediaStates.delete(token);
    return payload;
}

function bindNativeMediaStateLifecycle() {
    if (unsubscribeNativeMediaState) return;
    unsubscribeNativeMediaState = EventsOn("native_media_state", routeNativeMediaState);
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
    return Boolean(activeNative && !activeNative.htmlControls && activeNative.presentation !== "standalone");
}

function setChromeVisible(visible: boolean) {
    modalEl?.classList.toggle("is-video-chrome-visible", visible);
    modalEl?.classList.toggle("is-video-cursor-hidden", !visible && !hasError && !isNativeFallbackActive());
}

function setNativeLayout(layout: NativeLayout) {
    const visible = layout !== "none";
    const overlay = layout === "embedded-overlay";
    const fallback = layout === "embedded-fallback";
    const standalone = layout === "standalone";
    modalEl?.classList.toggle("is-video-native", visible);
    modalEl?.classList.toggle("is-video-native-fallback", fallback);
    modalEl?.classList.toggle("is-video-native-standalone", standalone);
    if (standaloneEl) standaloneEl.hidden = !standalone;
    document.documentElement.classList.toggle("native-video-active", overlay);
    document.body.classList.toggle("native-video-active", overlay);
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
    return Boolean(
        fullscreenRuntimeAvailable()
        && activeAdapter
        && activeNative?.presentation !== "standalone"
        && !hasError
    );
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
    if (!activeNative || activeNative.presentation === "standalone") return;
    if (nativeResizeFrame) cancelAnimationFrame(nativeResizeFrame);
    nativeResizeFrame = requestAnimationFrame(() => {
        nativeResizeFrame = 0;
        if (activeNative?.presentation === "standalone") return;
        const token = activeNative?.token || "";
        const rect = currentNativeRect();
        if (!token || !rect) return;
        void resizeNativeMedia(token, rect).catch((err) => {
            console.warn("ResizeNativeMedia failed:", err);
        });
    });
}

function scheduleNativeResizeAfterWindowTransition() {
    if (!activeNative || activeNative.presentation === "standalone") return;
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
    if (loadingStatusOverride) {
        loadingStatusEl.textContent = loadingStatusOverride;
        return;
    }
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

function setLoadingStatusOverride(message: string) {
    loadingStatusOverride = message;
    updateLoadingStatus();
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
    loadingStatusOverride = "";
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
    const customInput = byID<HTMLInputElement>("video-speed-custom-input");
    if (speedBtnEl) {
        speedBtnEl.textContent = `${formatRate(state.rate)}x`;
        speedBtnEl.title = isNativeFallbackActive() ? "Playback speed (click to cycle)" : "Playback speed";
        speedBtnEl.setAttribute("aria-label", speedBtnEl.title);
    }
    if (customInput && document.activeElement !== customInput) {
        customInput.value = formatRate(state.rate);
    }
    speedMenuEl?.querySelectorAll<HTMLButtonElement>("[data-rate]").forEach((button) => {
        const selected = Math.abs(Number(button.dataset.rate || 1) - state.rate) < 0.001;
        button.classList.toggle("is-selected", selected);
        button.setAttribute("aria-checked", selected ? "true" : "false");
    });
}

function syncNativeTrackControls(tracks: NativeMediaTrack[]) {
    if (!activeNative || activeNative.presentation === "standalone") {
        resetNativeTrackControls();
        return;
    }

    const audioTracks = tracks.filter((track) => track.type === "audio");
    const subtitleTracks = tracks.filter((track) => track.type === "subtitle");
    const showAudio = audioTracks.length > 1;
    const showSubtitles = subtitleTracks.length > 0;
    const showControls = showAudio || showSubtitles;
    const visibilityChanged = trackControlsEl?.hidden === showControls
        || audioControlEl?.hidden === showAudio
        || subtitleControlEl?.hidden === showSubtitles;

    if (trackControlsEl) trackControlsEl.hidden = !showControls;
    if (audioControlEl) audioControlEl.hidden = !showAudio;
    if (subtitleControlEl) subtitleControlEl.hidden = !showSubtitles;
    if (audioSelectEl) audioSelectEl.disabled = !showAudio;
    if (subtitleSelectEl) subtitleSelectEl.disabled = !showSubtitles;

    const signature = JSON.stringify({ token: activeNative.token, audioTracks, subtitleTracks });
    if (signature !== lastTrackSignature) {
        lastTrackSignature = signature;
        renderTrackOptions(audioSelectEl, audioTracks, false);
        renderTrackOptions(subtitleSelectEl, subtitleTracks, true);
    }
    syncTrackSelection(audioSelectEl, audioTracks, false);
    syncTrackSelection(subtitleSelectEl, subtitleTracks, true);

    if (visibilityChanged) scheduleNativeResize();
}

function renderTrackOptions(
    select: HTMLSelectElement | null,
    tracks: NativeMediaTrack[],
    includeOff: boolean,
) {
    if (!select) return;
    const options: HTMLOptionElement[] = [];
    if (includeOff) options.push(trackOption("no", "Off"));
    tracks.forEach((track, index) => {
        options.push(trackOption(String(track.id), nativeTrackLabel(track, index)));
    });
    select.replaceChildren(...options);
}

function trackOption(value: string, label: string) {
    const option = document.createElement("option");
    option.value = value;
    option.textContent = label;
    option.title = label;
    return option;
}

function syncTrackSelection(
    select: HTMLSelectElement | null,
    tracks: NativeMediaTrack[],
    includeOff: boolean,
) {
    if (!select) return;
    const selected = tracks.find((track) => track.selected);
    const fallback = tracks.find((track) => track.default) ?? tracks[0];
    select.value = selected ? String(selected.id) : includeOff ? "no" : fallback ? String(fallback.id) : "";
}

function resetNativeTrackControls() {
    const hadVisibleControls = Boolean(trackControlsEl && !trackControlsEl.hidden);
    const needsReset = hadVisibleControls
        || Boolean(audioControlEl && !audioControlEl.hidden)
        || Boolean(subtitleControlEl && !subtitleControlEl.hidden)
        || Boolean(audioSelectEl && (!audioSelectEl.disabled || audioSelectEl.options.length > 0))
        || Boolean(subtitleSelectEl && (!subtitleSelectEl.disabled || subtitleSelectEl.options.length > 0))
        || lastTrackSignature !== "";
    if (!needsReset) return;
    lastTrackSignature = "";
    trackControlsEl?.setAttribute("hidden", "");
    audioControlEl?.setAttribute("hidden", "");
    subtitleControlEl?.setAttribute("hidden", "");
    if (audioSelectEl) {
        audioSelectEl.disabled = true;
        audioSelectEl.replaceChildren();
    }
    if (subtitleSelectEl) {
        subtitleSelectEl.disabled = true;
        subtitleSelectEl.replaceChildren();
    }
    if (hadVisibleControls) scheduleNativeResize();
}

function selectedTrackID(select: HTMLSelectElement | null, type: NativeMediaTrack["type"]): number | null {
    if (!select) return null;
    const id = Number(select.value);
    if (!Number.isSafeInteger(id) || id <= 0) return null;
    return currentState.tracks.some((track) => track.type === type && track.id === id) ? id : null;
}

function selectAudioTrack() {
    if (!(activeAdapter instanceof NativeMpvAdapter)) return;
    const id = selectedTrackID(audioSelectEl, "audio");
    if (id === null) return;
    activeAdapter.setAudioTrack(id);
    revealChrome();
}

function selectSubtitleTrack() {
    if (!(activeAdapter instanceof NativeMpvAdapter) || !subtitleSelectEl) return;
    if (subtitleSelectEl.value === "no") {
        activeAdapter.setSubtitleTrack(null);
        revealChrome();
        return;
    }
    const id = selectedTrackID(subtitleSelectEl, "subtitle");
    if (id === null) return;
    activeAdapter.setSubtitleTrack(id);
    revealChrome();
}

function nextPlaybackRate(currentRate: number) {
    const current = Number.isFinite(currentRate) && currentRate > 0 ? currentRate : 1;
    const currentIndex = RATE_OPTIONS.findIndex((rate) => Math.abs(rate - current) < 0.001);
    if (currentIndex >= 0) return RATE_OPTIONS[(currentIndex + 1) % RATE_OPTIONS.length];
    const nextHigher = RATE_OPTIONS.find((rate) => rate > current);
    return nextHigher ?? RATE_OPTIONS[0];
}

function clampPlaybackRate(value: number) {
    return clamp(Number.isFinite(value) ? value : 1, MIN_PLAYBACK_RATE, MAX_PLAYBACK_RATE);
}

function parseCustomPlaybackRate(value: string) {
    const rate = Number(value.trim());
    if (!Number.isFinite(rate) || rate <= 0) return null;
    return clampPlaybackRate(rate);
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
    if (!state.loading && loadingStatusOverride) {
        loadingStatusOverride = "";
    }
    schedulePlaybackHint(state);
    syncButtonState(state);
    syncTimeline(state);
    syncVolume(state);
    syncSpeed(state);
    syncNativeTrackControls(state.tracks);
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
    const adapter = detachActiveReferences();
    if (adapter) {
        try {
            await adapter.close();
        } catch (err) {
            console.warn("Close media failed:", err);
        }
    }
}

function detachActiveReferences(): PlayerAdapter | null {
    const adapter = activeAdapter;
    const nativeToken = activeNative?.token ?? "";
    activeAdapter = null;
    if (adapter === activeNativeStateAdapter) activeNativeStateAdapter = null;
    activeNative = null;
    activeMediaToken = "";
    activeMediaEncrypted = false;
    unsubscribeState?.();
    unsubscribeState = null;
    clearPlaybackHintTimer();
    clearMediaStatsPolling();
    clearVolumeCommandFrame();
    clearSkipFeedback();
    resetThumbnailPreview();
    setSpeedMenuOpen(false);
    setNativeLayout("none");
    if (nativeToken) pendingNativeMediaStates.delete(nativeToken);
    currentState = { ...EMPTY_STATE };
    applyState(currentState);
    return adapter;
}

function detachHtmlForNative(adapter: HtmlVideoAdapter): MediaOpenResult | null {
    if (activeAdapter !== adapter) return null;
    const detached = detachActiveReferences();
    const opened = detached === adapter ? adapter.detachForNative() : null;
    activeMediaEncrypted = Boolean(opened?.info.encrypted);
    return opened;
}

async function safelyCloseMedia(token: string) {
    if (!token) return;
    try {
        await closeMedia(token);
    } catch (err) {
        console.warn("Close media session failed:", err);
    }
}

async function safelyCloseNativeMedia(token: string) {
    if (!token) return;
    try {
        await closeNativeMedia(token);
    } catch (err) {
        console.warn("Close native media session failed:", err);
    } finally {
        pendingNativeMediaStates.delete(token);
    }
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

async function prepareNativeRect(isCurrent: () => boolean): Promise<NativeMediaRect | null> {
    const measureFallback = shouldMeasureNativeFallbackBeforeOpen();
    // macOS can overlay the HTML controls above libmpv. Windows/Linux reserve
    // their HTML chrome before creating the native child surface.
    setNativeLayout(measureFallback ? "embedded-fallback" : "none");
    await nextFrame();
    if (!isCurrent() || !isOpen()) return null;
    const rect = currentNativeRect();
    if (rect) return rect;
    setNativeLayout("none");
    setError("Could not prepare the native video surface.");
    return null;
}

async function openHtmlPlayback(attempt: VideoOpenAttempt, isCurrent: () => boolean) {
    let opened: MediaOpenResult | null = null;
    let adapter: HtmlVideoAdapter | null = null;
    try {
        opened = await openMedia(attempt.target.id);
        if (!isCurrent() || !isOpen()) {
            await safelyCloseMedia(opened.token);
            return;
        }
        if (!opened.url || !opened.token) {
            await safelyCloseMedia(opened.token);
            opened = null;
            throw new Error("media session did not return a playable URL");
        }

        const displayName = opened.info.name || opened.name || attempt.target.name || "Video";
        const displaySize = opened.info.plaintextSize || opened.info.storedSize || attempt.target.size || 0;
        updateMediaText(displayName, displaySize);
        activeThumbnailURL = opened.thumbnailUrl;
        activeMediaToken = opened.token;
        activeMediaEncrypted = Boolean(opened.info.encrypted);
        thumbnailRequestSeq += 1;

        adapter = new HtmlVideoAdapter(videoEl!, opened, (code, state) => {
            handleHtmlMediaError(attempt, adapter!, code, state);
        });
        activeAdapter = adapter;
        unsubscribeState = adapter.subscribe((state) => {
            if (!isCurrent() || activeAdapter !== adapter) return;
            applyState(state);
        });
        adapter.load();
    } catch (err: unknown) {
        if (adapter && activeAdapter === adapter) {
            await releaseActive();
        } else if (opened?.token) {
            if (activeMediaToken === opened.token) detachActiveReferences();
            await safelyCloseMedia(opened.token);
        }
        if (!isCurrent()) return;
        console.error("OpenMedia failed:", err);
        setError(errorMessage(err, "Could not open this video."));
    }
}

function handleHtmlMediaError(
    attempt: VideoOpenAttempt,
    adapter: HtmlVideoAdapter,
    code: number | undefined,
    state: PlayerState,
) {
    if (
        attempt.htmlFailureHandled ||
        activeOpenAttempt !== attempt ||
        !playbackTransitions.isCurrent(attempt.generation) ||
        activeAdapter !== adapter
    ) {
        return;
    }
    attempt.htmlFailureHandled = true;

    if (shouldFallbackFromHtmlMediaError(code)) {
        attempt.nativeFallbackRequested = true;
        const intent = capturePlaybackIntent(state, attempt.pausedByUser);
        setLoadingStatusOverride("Switching to a compatible player...");
        setLoading(true);
        void promoteHtmlToNative(attempt, adapter, intent);
        return;
    }

    const message = code === 2
        ? "The video stream was interrupted. Check your connection and try again."
        : "The embedded player could not continue playing this video.";
    void playbackTransitions.run(attempt.generation, async (isCurrent) => {
        await releaseActive();
        if (isCurrent()) setError(message);
    });
}

async function promoteHtmlToNative(
    attempt: VideoOpenAttempt,
    adapter: HtmlVideoAdapter,
    intent: PlaybackIntent,
) {
    await playbackTransitions.run(attempt.generation, async (isCurrent) => {
        if (!isCurrent() || activeAdapter !== adapter || !attempt.nativeFallbackRequested) return;
        const existing = detachHtmlForNative(adapter);
        if (!existing) return;

        setLoadingStatusOverride("Switching to a compatible player...");
        setLoading(true);
        if (!isCurrent() || !isOpen()) {
            await safelyCloseMedia(existing.token);
            return;
        }
        const rect = await prepareNativeRect(isCurrent);
        if (!rect || !isCurrent()) {
            await safelyCloseMedia(existing.token);
            return;
        }
        await openNativePlayback(attempt, rect, isCurrent, existing, intent);
    });
}

async function openNativePlayback(
    attempt: VideoOpenAttempt,
    rect: NativeMediaRect,
    isCurrent: () => boolean,
    existing: MediaOpenResult | null = null,
    intent: PlaybackIntent | null = null,
) {
    let opened: NativeMediaOpenResult | null = null;
    let owner: "none" | "html" | "native" | "adapter" = existing ? "html" : "none";
    try {
        const result = existing
            ? await attachNativeMedia(existing.token, rect)
            : await openNativeMedia(attempt.target.id, rect);
        opened = result;
        owner = "native";
        if (existing && opened.token !== existing.token) {
            throw new Error("native media attachment returned a different session token");
        }

        if (!opened.token) {
            throw new Error("native media session did not return a token");
        }
        if (!isCurrent() || !isOpen()) {
            await safelyCloseNativeMedia(opened.token);
            return;
        }

        activateNativePlayback(attempt, opened, intent);
        owner = "adapter";
    } catch (err: unknown) {
        const cleanupToken = opened?.token || existing?.token || "";
        if (cleanupToken && activeMediaToken === cleanupToken && activeAdapter) {
            await releaseActive();
        } else if (owner === "native") {
            if (cleanupToken && activeMediaToken === cleanupToken) detachActiveReferences();
            await safelyCloseNativeMedia(cleanupToken);
            if (existing && opened?.token && opened.token !== existing.token) {
                await safelyCloseMedia(existing.token);
            }
        } else if (owner === "html") {
            await safelyCloseMedia(existing?.token || "");
        }
        if (!isCurrent()) return;
        console.error(existing ? "AttachNativeMedia failed:" : "OpenNativeMedia failed:", err);
        setNativeLayout("none");
        setError(errorMessage(err, existing
            ? "This video could not be opened by the compatible player."
            : "Could not open this video."));
    }
}

function handleNativeMediaError(attempt: VideoOpenAttempt, token: string, detail: string) {
    if (
        activeOpenAttempt !== attempt
        || !playbackTransitions.isCurrent(attempt.generation)
        || activeNative?.token !== token
        || !(activeAdapter instanceof NativeMpvAdapter)
    ) {
        return;
    }
    console.error("Native media player failed:", detail);
    void playbackTransitions.run(attempt.generation, async (isCurrent) => {
        if (!isCurrent() || activeNative?.token !== token) return;
        if (isOpen()) {
            setError("The compatible player stopped unexpectedly. Try opening the video again.");
        }
        await releaseActive();
    });
}

function handleNativeMediaClosed(attempt: VideoOpenAttempt, token: string) {
    if (
        activeOpenAttempt !== attempt
        || !playbackTransitions.isCurrent(attempt.generation)
        || activeNative?.token !== token
        || !(activeAdapter instanceof NativeMpvAdapter)
    ) {
        return;
    }
    void closeVideoModal();
}

function activateNativePlayback(
    attempt: VideoOpenAttempt,
    opened: NativeMediaOpenResult,
    intent: PlaybackIntent | null,
) {
    const standalone = opened.presentation === "standalone";
    setNativeLayout(standalone ? "standalone" : opened.htmlControls ? "embedded-overlay" : "embedded-fallback");
    activeNative = opened;
    activeMediaToken = opened.token;
    activeMediaEncrypted = Boolean(opened.info.encrypted);
    const displayName = opened.info.name || opened.name || attempt.target.name || "Video";
    const displaySize = opened.info.plaintextSize || opened.info.storedSize || attempt.target.size || 0;
    updateMediaText(displayName, displaySize);
    activeThumbnailURL = opened.thumbnailUrl;
    thumbnailRequestSeq += 1;

    const adapter = new NativeMpvAdapter(opened, (detail) => {
        handleNativeMediaError(attempt, opened.token, detail);
    }, () => {
        handleNativeMediaClosed(attempt, opened.token);
    });
    activeAdapter = adapter;
    activeNativeStateAdapter = adapter;
    adapter.start(takePendingNativeMediaState(opened.token));
    if (adapter.isTerminal()) return;
    if (intent) {
        adapter.setVolume(intent.volume);
        adapter.setMuted(intent.muted);
        adapter.setSpeed(intent.rate);
        if (intent.paused) adapter.playPause();
    }

    let positionRestored = !intent || intent.currentTime <= 0;
    unsubscribeState = adapter.subscribe((state) => {
        if (!playbackTransitions.isCurrent(attempt.generation) || activeAdapter !== adapter) return;
        applyState(state);
        if (!positionRestored && state.duration > 0) {
            positionRestored = true;
            adapter.seekAbsolute(intent!.currentTime);
        }
    });
    setChromeVisible(true);
    if (!standalone) scheduleNativeResize();
}

export async function openVideoModal(target: VideoOpenTarget) {
    if (!modalEl || !videoEl || !filenameEl || !metaEl) return;
    const id = Number(target.id || 0);
    if (!id) return;

    const attempt: VideoOpenAttempt = {
        generation: playbackTransitions.begin(),
        target: { ...target, id },
        htmlFailureHandled: false,
        nativeFallbackRequested: false,
        pausedByUser: false,
    };
    activeOpenAttempt = attempt;

    updateMediaText(target.name || "Video", target.size || 0);
    clearError();
    setLoadingStatusOverride("");
    setLoading(true);
    setChromeVisible(true);
    modalEl.style.display = "flex";
    modalEl.setAttribute("aria-hidden", "false");
    a11y?.activate();
    void syncFullscreenState();

    return playbackTransitions.run(attempt.generation, async (isCurrent) => {
        await releaseActive();
        if (!isCurrent() || !isOpen()) return;
        setLoadingStatusOverride("");
        setLoading(true);
        if (isWebviewDirectVideo(attempt.target.name)) {
            await openHtmlPlayback(attempt, isCurrent);
            return;
        }
        const rect = await prepareNativeRect(isCurrent);
        if (!rect || !isCurrent()) return;
        await openNativePlayback(attempt, rect, isCurrent);
    });
}

export async function closeVideoModal() {
    if (!modalEl) return;
    const generation = playbackTransitions.begin();
    activeOpenAttempt = null;
    clearChromeTimer();
    await exitVideoFullscreen();
    if (!playbackTransitions.isCurrent(generation)) return;
    modalEl.style.display = "none";
    modalEl.setAttribute("aria-hidden", "true");
    a11y?.deactivate();
    await playbackTransitions.run(generation, async () => releaseActive());
    if (!playbackTransitions.isCurrent(generation)) return;
    clearError();
    setLoadingStatusOverride("");
    setLoading(false);
}

function togglePlayback() {
    if (!activeAdapter || hasError) return;
    if (activeOpenAttempt) {
        activeOpenAttempt.pausedByUser = !currentState.paused;
    }
    activeAdapter.playPause();
    revealChrome();
}

function seekBy(delta: number) {
    if (!activeAdapter || (currentState.duration <= 0 && !activeNative)) return;
    activeAdapter.seekRelative(delta);
    showSkipFeedback(delta);
    revealChrome();
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
    speedMenuEl?.addEventListener("submit", (event) => {
        event.preventDefault();
        event.stopPropagation();
        if (!activeAdapter) return;
        const input = byID<HTMLInputElement>("video-speed-custom-input");
        const rate = input ? parseCustomPlaybackRate(input.value) : null;
        if (rate == null) {
            input?.focus({ preventScroll: true });
            return;
        }
        activeAdapter.setSpeed(rate);
        closeSpeedMenu(true);
        revealChrome();
    });
    speedMenuEl?.addEventListener("keydown", (event) => {
        if (!isSpeedMenuOpen()) return;
        const target = event.target as HTMLElement | null;
        if (target?.closest(".video-speed-custom")) {
            if (event.key === "Escape") {
                event.preventDefault();
                event.stopPropagation();
                closeSpeedMenu(true);
            }
            return;
        }
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
    if (activeNative?.presentation === "standalone") return;
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
    if (activeNative?.presentation === "standalone") return;
    if (targetIsVideoChrome(event.target)) return;
    togglePlayback();
}

function handleWindowResize() {
    if (activeNative?.presentation !== "standalone") scheduleNativeResize();
    void syncFullscreenState();
}

function bindEncryptedMediaLifecycle() {
    if (unsubscribeEncryptedMediaSessionsClosed) return;
    unsubscribeEncryptedMediaSessionsClosed = EventsOn("encrypted_media_sessions_closed", () => {
        if (!activeOpenAttempt || (!activeOpenAttempt.target.encrypted && !activeMediaEncrypted)) return;
        void closeVideoModal();
    });
}

function bindControls() {
    closeBtnEl?.addEventListener("click", () => void closeVideoModal());
    centerPlayBtnEl?.addEventListener("click", togglePlayback);
    centerSkipBackBtnEl?.addEventListener("click", () => seekBy(-SEEK_STEP_SECONDS));
    centerSkipForwardBtnEl?.addEventListener("click", () => seekBy(SEEK_STEP_SECONDS));
    skipBackBtnEl?.addEventListener("click", () => seekBy(-SEEK_STEP_SECONDS));
    skipForwardBtnEl?.addEventListener("click", () => seekBy(SEEK_STEP_SECONDS));
    playBtnEl?.addEventListener("click", togglePlayback);
    audioSelectEl?.addEventListener("change", selectAudioTrack);
    subtitleSelectEl?.addEventListener("change", selectSubtitleTrack);
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
    const options = RATE_OPTIONS.map(speedOptionMarkup).join("");
    speedMenuEl.innerHTML = `${options}${customSpeedMarkup()}`;
}

function speedOptionMarkup(rate: number) {
    return `<button type="button" role="menuitemradio" data-rate="${rate}" aria-checked="${rate === 1 ? "true" : "false"}"><span class="video-speed-check" aria-hidden="true">✓</span><span>${formatRate(rate)}x</span></button>`;
}

function customSpeedMarkup() {
    return `<form class="video-speed-custom" role="none" aria-label="Custom playback speed"><label for="video-speed-custom-input">Custom</label><div class="video-speed-custom-row"><input id="video-speed-custom-input" type="number" inputmode="decimal" min="${MIN_PLAYBACK_RATE}" max="${MAX_PLAYBACK_RATE}" step="0.05" value="1" aria-label="Custom playback speed" /><span aria-hidden="true">x</span><button type="submit">Set</button></div></form>`;
}

export function setupVideoModal() {
    if (videoSetupComplete) return;
    const host = byID<HTMLElement>("video-modal");
    if (host && !videoMarkupHandle) {
        host.replaceChildren();
        videoMarkupHandle = mountSvelte(VideoModal, { target: host, props: {} });
    }

    modalEl = byID("video-modal");
    stageEl = byID("video-stage");
    topbarEl = document.querySelector<HTMLElement>("#video-modal .video-topbar");
    controlsEl = document.querySelector<HTMLElement>("#video-modal .video-controls");
    filenameEl = byID("video-filename");
    metaEl = byID("video-meta");
    closeBtnEl = byID("video-close");
    nativeViewportEl = byID("video-native-viewport");
    standaloneEl = byID("video-standalone");
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
    trackControlsEl = byID("video-track-controls");
    audioControlEl = byID("video-audio-control");
    audioSelectEl = byID("video-audio-select");
    subtitleControlEl = byID("video-subtitle-control");
    subtitleSelectEl = byID("video-subtitle-select");

    if (!modalEl || !videoEl || !stageEl) {
        console.error("Video modal setup failed. Missing #video-modal, #video-stage, or #video-player.");
        return;
    }
    videoSetupComplete = true;
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
    bindEncryptedMediaLifecycle();
    bindNativeMediaStateLifecycle();
    renderSpeedOptions();
    bindControls();
    applyState(EMPTY_STATE);
}
