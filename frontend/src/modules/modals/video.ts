import {
    closeMedia,
    closeNativeMedia,
    nativeMediaCommand,
    openMedia,
    openNativeMedia,
    resizeNativeMedia,
    type MediaOpenResult,
    type NativeMediaOpenResult,
    type NativeMediaRect,
} from "../../api";
import { formatBytes } from "../../utils";
import { isWebviewDirectVideo, videoFormatLabel } from "../media-types";
import { installModalA11y } from "./modal-a11y";

const CHROME_HIDE_DELAY_MS = 2500;
const LOADING_DEBOUNCE_MS = 250;
const SEEK_STEP_SECONDS = 10;
const VOLUME_STEP = 0.05;
const RATE_OPTIONS = [0.5, 0.75, 1, 1.25, 1.5, 2];

interface VideoOpenTarget {
    id: number;
    name: string;
    size?: number;
    encrypted?: boolean;
}

interface PlayerState {
    paused: boolean;
    currentTime: number;
    duration: number;
    bufferedEnd: number;
    volume: number;
    muted: boolean;
    rate: number;
    loading: boolean;
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
    bufferedEnd: 0,
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
        const bufferedEnd = bufferedEndFor(this.video, currentTime);
        return {
            paused: this.video.paused,
            currentTime,
            duration,
            bufferedEnd,
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
    private state = { ...EMPTY_STATE };
    private closed = false;

    constructor(private readonly opened: NativeMediaOpenResult) {}

    subscribe(callback: (state: PlayerState) => void) {
        this.subscribers.add(callback);
        callback(this.state);
        return () => this.subscribers.delete(callback);
    }

    playPause() {
        void this.command(["cycle", "pause"]);
    }

    seekAbsolute(_seconds: number) {
        // Native playback keeps mpv's own controls until we have an evented state
        // bridge. The backend command whitelist intentionally exposes relative
        // seeks only, so do not synthesize unsupported absolute commands here.
    }

    seekRelative(seconds: number) {
        void this.command(["seek", String(seconds), "relative"]);
    }

    setVolume(_value: number) {
        // Native state transport lands in the next slice; keep mpv OSC for native UI until then.
    }

    setMuted(_value: boolean) {
        void this.command(["cycle", "mute"]);
    }

    setSpeed(_value: number) {
        // Native state transport lands in the next slice; keep mpv OSC for native UI until then.
    }

    async close() {
        if (this.closed) return;
        this.closed = true;
        this.subscribers.clear();
        await closeNativeMedia(this.opened.token);
    }

    private async command(command: string[]) {
        if (this.closed) return;
        try {
            await nativeMediaCommand(this.opened.token, command);
        } catch (err) {
            console.warn("NativeMediaCommand failed:", err);
        }
    }
}

let modalEl: HTMLElement | null = null;
let stageEl: HTMLElement | null = null;
let filenameEl: HTMLElement | null = null;
let metaEl: HTMLElement | null = null;
let closeBtnEl: HTMLButtonElement | null = null;
let nativeViewportEl: HTMLElement | null = null;
let videoEl: HTMLVideoElement | null = null;
let loadingEl: HTMLElement | null = null;
let errorEl: HTMLElement | null = null;
let playBtnEl: HTMLButtonElement | null = null;
let muteBtnEl: HTMLButtonElement | null = null;
let scrubberEl: HTMLElement | null = null;
let scrubberPlayedEl: HTMLElement | null = null;
let scrubberBufferedEl: HTMLElement | null = null;
let scrubberThumbEl: HTMLElement | null = null;
let scrubberTooltipEl: HTMLElement | null = null;
let volumeSliderEl: HTMLElement | null = null;
let volumeFillEl: HTMLElement | null = null;
let volumeThumbEl: HTMLElement | null = null;
let timeEl: HTMLElement | null = null;
let durationEl: HTMLElement | null = null;
let speedBtnEl: HTMLButtonElement | null = null;
let speedMenuEl: HTMLElement | null = null;

let activeAdapter: PlayerAdapter | null = null;
let activeNative: NativeMediaOpenResult | null = null;
let unsubscribeState: (() => void) | null = null;
let currentState: PlayerState = { ...EMPTY_STATE };
let openSeq = 0;
let chromeHideTimer: ReturnType<typeof setTimeout> | null = null;
let loadingTimer: ReturnType<typeof setTimeout> | null = null;
let nativeResizeFrame = 0;
let seekingWithPointer = false;
let volumeDragging = false;
let hasError = false;
let a11y: ReturnType<typeof installModalA11y> | null = null;

function byID<T extends HTMLElement>(id: string): T | null {
    return document.getElementById(id) as T | null;
}

function clamp(value: number, min: number, max: number) {
    return Math.max(min, Math.min(max, Number.isFinite(value) ? value : min));
}

function percent(value: number, total: number) {
    return total > 0 ? clamp((value / total) * 100, 0, 100) : 0;
}

function bufferedEndFor(video: HTMLVideoElement, currentTime: number) {
    for (let i = 0; i < video.buffered.length; i += 1) {
        const start = video.buffered.start(i);
        const end = video.buffered.end(i);
        if (currentTime >= start && currentTime <= end) return end;
    }
    return video.buffered.length > 0 ? video.buffered.end(video.buffered.length - 1) : 0;
}

function isOpen() {
    return Boolean(modalEl && modalEl.style.display !== "none");
}

function errorMessage(err: unknown, fallback: string) {
    if (err instanceof Error && err.message) return err.message;
    return String(err || fallback);
}

function setChromeVisible(visible: boolean) {
    modalEl?.classList.toggle("is-video-chrome-visible", visible);
    modalEl?.classList.toggle("is-video-cursor-hidden", !visible && !hasError && !activeNative);
}

function setNativeMode(visible: boolean) {
    modalEl?.classList.toggle("is-video-native", visible);
}

function nextFrame(): Promise<void> {
    return new Promise((resolve) => requestAnimationFrame(() => resolve()));
}

function currentNativeRect(): NativeMediaRect | null {
    if (!nativeViewportEl) return null;
    const rect = nativeViewportEl.getBoundingClientRect();
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

function clearChromeTimer() {
    if (!chromeHideTimer) return;
    clearTimeout(chromeHideTimer);
    chromeHideTimer = null;
}

function scheduleChromeHide() {
    clearChromeTimer();
    if (!isOpen() || currentState.paused || hasError || activeNative || isSpeedMenuOpen()) return;
    chromeHideTimer = setTimeout(() => {
        if (!isOpen() || currentState.paused || hasError || activeNative || isSpeedMenuOpen()) return;
        setChromeVisible(false);
    }, CHROME_HIDE_DELAY_MS);
}

function revealChrome() {
    if (!isOpen()) return;
    setChromeVisible(true);
    scheduleChromeHide();
}

function setLoading(visible: boolean) {
    if (!loadingEl) return;
    if (loadingTimer) {
        clearTimeout(loadingTimer);
        loadingTimer = null;
    }
    if (!visible) {
        loadingEl.style.display = "none";
        loadingEl.setAttribute("aria-hidden", "true");
        return;
    }
    loadingTimer = setTimeout(() => {
        loadingTimer = null;
        if (!loadingEl || hasError) return;
        loadingEl.style.display = "flex";
        loadingEl.setAttribute("aria-hidden", "false");
    }, LOADING_DEBOUNCE_MS);
}

function setError(message: string) {
    hasError = true;
    setLoading(false);
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
    const buffered = percent(state.bufferedEnd, state.duration);
    if (scrubberPlayedEl && !seekingWithPointer) scrubberPlayedEl.style.width = `${played}%`;
    if (scrubberBufferedEl) scrubberBufferedEl.style.width = `${buffered}%`;
    if (scrubberThumbEl && !seekingWithPointer) scrubberThumbEl.style.left = `${played}%`;
    if (scrubberEl) {
        scrubberEl.classList.toggle("is-disabled", state.duration <= 0);
        setSliderARIA(scrubberEl, state.currentTime, 0, Math.max(0, state.duration), `${formatTime(state.currentTime)} of ${state.duration > 0 ? formatTime(state.duration) : "unknown"}`);
    }
}

function syncVolume(state: PlayerState) {
    const value = state.muted ? 0 : state.volume;
    if (volumeFillEl) volumeFillEl.style.width = `${clamp(value, 0, 1) * 100}%`;
    if (volumeThumbEl) volumeThumbEl.style.left = `${clamp(value, 0, 1) * 100}%`;
    setSliderARIA(volumeSliderEl, value * 100, 0, 100, `${Math.round(value * 100)}%`);
}

function syncSpeed(state: PlayerState) {
    if (speedBtnEl) speedBtnEl.textContent = `${formatRate(state.rate)}x`;
    speedMenuEl?.querySelectorAll<HTMLButtonElement>("[data-rate]").forEach((button) => {
        const selected = Number(button.dataset.rate || 1) === state.rate;
        button.classList.toggle("is-selected", selected);
        button.setAttribute("aria-checked", selected ? "true" : "false");
    });
}

function applyState(state: PlayerState) {
    const wasPaused = currentState.paused;
    currentState = state;
    syncButtonState(state);
    syncTimeline(state);
    syncVolume(state);
    syncSpeed(state);
    setLoading(state.loading);
    if (state.paused || hasError) {
        clearChromeTimer();
        setChromeVisible(true);
    } else if (wasPaused) {
        scheduleChromeHide();
    }
}

function formatRate(rate: number) {
    return Number.isInteger(rate) ? String(rate) : String(rate).replace(/0+$/, "").replace(/\.$/, "");
}

function scrubberSecondsFromEvent(event: PointerEvent | MouseEvent) {
    if (!scrubberEl || currentState.duration <= 0) return 0;
    const rect = scrubberEl.getBoundingClientRect();
    const ratio = clamp((event.clientX - rect.left) / Math.max(1, rect.width), 0, 1);
    return ratio * currentState.duration;
}

function previewScrubber(event: PointerEvent | MouseEvent) {
    if (!scrubberEl || !scrubberTooltipEl || currentState.duration <= 0) return;
    const rect = scrubberEl.getBoundingClientRect();
    const ratio = clamp((event.clientX - rect.left) / Math.max(1, rect.width), 0, 1);
    const seconds = ratio * currentState.duration;
    scrubberTooltipEl.textContent = formatTime(seconds);
    scrubberTooltipEl.style.left = `${ratio * 100}%`;
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
    activeAdapter?.setVolume(value);
    revealChrome();
}

async function releaseActive() {
    const adapter = activeAdapter;
    activeAdapter = null;
    activeNative = null;
    unsubscribeState?.();
    unsubscribeState = null;
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
    unsubscribeState?.();
    unsubscribeState = null;
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
    if (metaEl) metaEl.textContent = `${videoFormatLabel(name)}${size ? ` · ${formatBytes(size)}` : ""}`;
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

    if (target.encrypted) {
        setError("Encrypted videos can't be played yet.");
        return;
    }
    if (!isWebviewDirectVideo(target.name)) {
        setNativeMode(true);
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
            activeNative = opened;
            const displayName = opened.info.name || opened.name || target.name || "Video";
            const displaySize = opened.info.plaintextSize || opened.info.storedSize || target.size || 0;
            updateMediaText(displayName, displaySize);
            const adapter = new NativeMpvAdapter(opened);
            activeAdapter = adapter;
            unsubscribeState = adapter.subscribe(applyState);
            setLoading(false);
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
        if (!seekingWithPointer) scrubberEl?.classList.remove("is-hovered");
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
        const handled = ["ArrowLeft", "ArrowDown", "ArrowRight", "ArrowUp"].includes(event.key);
        if (!handled) return;
        event.preventDefault();
        event.stopPropagation();
        if (!activeAdapter) return;
        if (event.key === "ArrowLeft" || event.key === "ArrowDown") {
            activeAdapter.setVolume(currentState.volume - VOLUME_STEP);
        } else if (event.key === "ArrowRight" || event.key === "ArrowUp") {
            activeAdapter.setVolume(currentState.volume + VOLUME_STEP);
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
    speedBtnEl?.setAttribute("aria-expanded", open ? "true" : "false");
    speedMenuEl?.classList.toggle("is-open", open);
    if (open) {
        clearChromeTimer();
        requestAnimationFrame(() => selectedSpeedButton()?.focus({ preventScroll: true }));
    } else if (isOpen() && !currentState.paused && !hasError && !activeNative) {
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

function targetShouldUseOwnKeyboard(target: HTMLElement | null) {
    if (!target) return false;
    if (target.isContentEditable) return true;
    const tag = String(target.tagName || "").toUpperCase();
    if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") return true;
    return Boolean(target.closest("#video-scrubber, #video-volume-slider, #video-speed-button, #video-speed-menu"));
}

function handleVideoShortcut(event: KeyboardEvent) {
    if (!isOpen()) return;
    const target = event.target as HTMLElement | null;
    if (targetShouldUseOwnKeyboard(target)) return;
    if (event.code === "Space" || event.key === " ") {
        event.preventDefault();
        togglePlayback();
    } else if (event.key === "ArrowLeft") {
        event.preventDefault();
        seekBy(-SEEK_STEP_SECONDS);
    } else if (event.key === "ArrowRight") {
        event.preventDefault();
        seekBy(SEEK_STEP_SECONDS);
    } else if (event.key === "ArrowUp" && !activeNative) {
        event.preventDefault();
        activeAdapter?.setVolume(currentState.volume + VOLUME_STEP);
        revealChrome();
    } else if (event.key === "ArrowDown" && !activeNative) {
        event.preventDefault();
        activeAdapter?.setVolume(currentState.volume - VOLUME_STEP);
        revealChrome();
    } else if (event.key.toLowerCase() === "m") {
        event.preventDefault();
        activeAdapter?.setMuted(!currentState.muted);
        revealChrome();
    }
}

function handleVideoPointerMove() {
    revealChrome();
}

function handleVideoClick() {
    togglePlayback();
}

function handleWindowResize() {
    scheduleNativeResize();
}

function bindControls() {
    closeBtnEl?.addEventListener("click", () => void closeVideoModal());
    playBtnEl?.addEventListener("click", togglePlayback);
    bindScrubber();
    bindVolume();
    bindSpeedMenu();
    modalEl?.addEventListener("pointermove", handleVideoPointerMove);
    videoEl?.addEventListener("click", handleVideoClick);
    document.addEventListener("keydown", handleVideoShortcut);
    window.addEventListener("resize", handleWindowResize);
}

function renderSpeedOptions() {
    if (!speedMenuEl) return;
    speedMenuEl.innerHTML = RATE_OPTIONS.map((rate) => (
        `<button type="button" role="menuitemradio" data-rate="${rate}" aria-checked="${rate === 1 ? "true" : "false"}">${formatRate(rate)}x</button>`
    )).join("");
}

export function setupVideoModal() {
    modalEl = byID("video-modal");
    stageEl = byID("video-stage");
    filenameEl = byID("video-filename");
    metaEl = byID("video-meta");
    closeBtnEl = byID("video-close");
    nativeViewportEl = byID("video-native-viewport");
    videoEl = byID("video-player");
    loadingEl = byID("video-loading");
    errorEl = byID("video-error");
    playBtnEl = byID("video-play");
    muteBtnEl = byID("video-mute");
    scrubberEl = byID("video-scrubber");
    scrubberPlayedEl = byID("video-scrubber-played");
    scrubberBufferedEl = byID("video-scrubber-buffered");
    scrubberThumbEl = byID("video-scrubber-thumb");
    scrubberTooltipEl = byID("video-scrubber-tooltip");
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
