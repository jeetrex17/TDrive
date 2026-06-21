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

const CHROME_HIDE_DELAY_MS = 1800;
const SEEK_STEP_SECONDS = 10;
const RATE_OPTIONS = [0.5, 0.75, 1, 1.25, 1.5, 2];

interface VideoOpenTarget {
    id: number;
    name: string;
    size?: number;
    encrypted?: boolean;
}

let modalEl: HTMLElement | null = null;
let shellEl: HTMLElement | null = null;
let filenameEl: HTMLElement | null = null;
let metaEl: HTMLElement | null = null;
let closeBtnEl: HTMLButtonElement | null = null;
let nativeViewportEl: HTMLElement | null = null;
let videoEl: HTMLVideoElement | null = null;
let loadingEl: HTMLElement | null = null;
let errorEl: HTMLElement | null = null;
let playBtnEl: HTMLButtonElement | null = null;
let muteBtnEl: HTMLButtonElement | null = null;
let progressEl: HTMLInputElement | null = null;
let volumeEl: HTMLInputElement | null = null;
let timeEl: HTMLElement | null = null;
let durationEl: HTMLElement | null = null;
let speedEl: HTMLSelectElement | null = null;

let active: MediaOpenResult | null = null;
let activeNative: NativeMediaOpenResult | null = null;
let openSeq = 0;
let chromeHideTimer: ReturnType<typeof setTimeout> | null = null;
let nativeResizeFrame = 0;
let seekingWithPointer = false;
let hasError = false;
let a11y: ReturnType<typeof installModalA11y> | null = null;

function byID<T extends HTMLElement>(id: string): T | null {
    return document.getElementById(id) as T | null;
}

function isOpen() {
    return Boolean(modalEl && modalEl.style.display !== "none");
}

function setChromeVisible(visible: boolean) {
    modalEl?.classList.toggle("is-video-chrome-visible", visible);
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
    if (!isOpen() || videoEl?.paused || hasError) return;
    chromeHideTimer = setTimeout(() => {
        if (!isOpen() || videoEl?.paused || hasError) return;
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
    loadingEl.style.display = visible ? "flex" : "none";
    loadingEl.setAttribute("aria-hidden", visible ? "false" : "true");
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

function setButtonState() {
    if (!videoEl || !playBtnEl || !muteBtnEl) return;
    playBtnEl.dataset.state = videoEl.paused ? "paused" : "playing";
    playBtnEl.setAttribute("aria-label", videoEl.paused ? "Play" : "Pause");
    playBtnEl.title = videoEl.paused ? "Play (Space)" : "Pause (Space)";
    muteBtnEl.dataset.state = videoEl.muted || videoEl.volume === 0 ? "muted" : "unmuted";
    muteBtnEl.setAttribute("aria-label", videoEl.muted ? "Unmute" : "Mute");
    muteBtnEl.title = videoEl.muted ? "Unmute" : "Mute";
}

function syncTimeline() {
    if (!videoEl) return;
    const duration = Number.isFinite(videoEl.duration) ? videoEl.duration : 0;
    const current = Number.isFinite(videoEl.currentTime) ? videoEl.currentTime : 0;
    if (timeEl) timeEl.textContent = formatTime(current);
    if (durationEl) durationEl.textContent = duration > 0 ? formatTime(duration) : "--:--";
    if (progressEl && !seekingWithPointer) {
        progressEl.max = duration > 0 ? String(duration) : "0";
        progressEl.value = String(Math.min(current, duration || current));
        progressEl.disabled = duration <= 0;
    }
}

function syncVolume() {
    if (!videoEl || !volumeEl) return;
    volumeEl.value = String(Math.round((videoEl.muted ? 0 : videoEl.volume) * 100));
    setButtonState();
}

function loadIntoVideo(opened: MediaOpenResult, target: VideoOpenTarget) {
    if (!videoEl) return;
    clearError();
    setLoading(true);
    videoEl.pause();
    videoEl.removeAttribute("src");
    videoEl.load();
    videoEl.src = opened.url;
    videoEl.playbackRate = 1;
    syncTimeline();
    syncVolume();

    const playPromise = videoEl.play();
    if (playPromise && typeof playPromise.catch === "function") {
        playPromise.catch(() => {
            // Autoplay may be blocked by the platform; showing the first frame
            // with an explicit play button is a good state, not an error.
            setLoading(false);
            setButtonState();
            revealChrome();
        });
    }
}

async function releaseActive() {
    const token = active?.token || "";
    const nativeToken = activeNative?.token || "";
    active = null;
    activeNative = null;
    setNativeMode(false);
    if (videoEl) {
        videoEl.pause();
        videoEl.removeAttribute("src");
        videoEl.load();
    }
    if (token) {
        try {
            await closeMedia(token);
        } catch (err) {
            console.warn("CloseMedia failed:", err);
        }
    }
    if (nativeToken) {
        try {
            await closeNativeMedia(nativeToken);
        } catch (err) {
            console.warn("CloseNativeMedia failed:", err);
        }
    }
}

function releaseActiveSoon() {
    const token = active?.token || "";
    const nativeToken = activeNative?.token || "";
    active = null;
    activeNative = null;
    setNativeMode(false);
    if (videoEl) {
        videoEl.pause();
        videoEl.removeAttribute("src");
        videoEl.load();
    }
    if (token) {
        void closeMedia(token).catch((err) => {
            console.warn("CloseMedia failed:", err);
        });
    }
    if (nativeToken) {
        void closeNativeMedia(nativeToken).catch((err) => {
            console.warn("CloseNativeMedia failed:", err);
        });
    }
}

export async function openVideoModal(target: VideoOpenTarget) {
    if (!modalEl || !videoEl || !filenameEl || !metaEl) return;
    const id = Number(target.id || 0);
    if (!id) return;

    const seq = ++openSeq;
    releaseActiveSoon();

    filenameEl.textContent = target.name || "Video";
    metaEl.textContent = `${videoFormatLabel(target.name)}${target.size ? ` · ${formatBytes(target.size)}` : ""}`;
    if (speedEl) speedEl.value = "1";
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
            filenameEl.textContent = opened.info.name || opened.name || target.name || "Video";
            const displaySize = opened.info.plaintextSize || opened.info.storedSize || target.size || 0;
            metaEl.textContent = `${videoFormatLabel(opened.info.name || opened.name || target.name)}${displaySize ? ` · ${formatBytes(displaySize)}` : ""}`;
            setLoading(false);
            scheduleNativeResize();
        } catch (err: any) {
            console.error("OpenNativeMedia failed:", err);
            setNativeMode(false);
            setError(String(err?.message || err || "Could not open this video."));
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
        active = opened;
        filenameEl.textContent = opened.info.name || opened.name || target.name || "Video";
        const displaySize = opened.info.plaintextSize || opened.info.storedSize || target.size || 0;
        metaEl.textContent = `${videoFormatLabel(opened.info.name || opened.name || target.name)}${displaySize ? ` · ${formatBytes(displaySize)}` : ""}`;
        loadIntoVideo(opened, target);
    } catch (err: any) {
        console.error("OpenMedia failed:", err);
        setError(String(err?.message || err || "Could not open this video."));
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
    syncTimeline();
}

function togglePlayback() {
    if (activeNative?.token) {
        void nativeMediaCommand(activeNative.token, ["cycle", "pause"]).catch((err) => {
            console.warn("NativeMediaCommand failed:", err);
        });
        revealChrome();
        return;
    }
    if (!videoEl || hasError) return;
    if (videoEl.paused) {
        videoEl.play().catch((err) => setError(String(err?.message || err || "Playback failed.")));
    } else {
        videoEl.pause();
    }
    revealChrome();
}

function seekBy(delta: number) {
    if (activeNative?.token) {
        void nativeMediaCommand(activeNative.token, ["seek", String(delta), "relative"]).catch((err) => {
            console.warn("NativeMediaCommand failed:", err);
        });
        revealChrome();
        return;
    }
    if (!videoEl || !Number.isFinite(videoEl.duration)) return;
    videoEl.currentTime = Math.max(0, Math.min(videoEl.duration, videoEl.currentTime + delta));
    syncTimeline();
    revealChrome();
}

function bindVideoEvents() {
    if (!videoEl) return;
    videoEl.addEventListener("loadstart", () => setLoading(true));
    videoEl.addEventListener("loadedmetadata", () => {
        setLoading(false);
        syncTimeline();
    });
    videoEl.addEventListener("canplay", () => setLoading(false));
    videoEl.addEventListener("waiting", () => setLoading(true));
    videoEl.addEventListener("playing", () => {
        setLoading(false);
        setButtonState();
        scheduleChromeHide();
    });
    videoEl.addEventListener("pause", () => {
        setButtonState();
        revealChrome();
    });
    videoEl.addEventListener("timeupdate", syncTimeline);
    videoEl.addEventListener("durationchange", syncTimeline);
    videoEl.addEventListener("volumechange", syncVolume);
    videoEl.addEventListener("error", () => {
        const code = videoEl?.error?.code;
        const hint = code ? ` (media error ${code})` : "";
        setError(`This format could not be decoded by the embedded webview${hint}.`);
        void releaseActive();
    });
}

function bindControls() {
    closeBtnEl?.addEventListener("click", () => void closeVideoModal());
    playBtnEl?.addEventListener("click", togglePlayback);
    muteBtnEl?.addEventListener("click", () => {
        if (!videoEl) return;
        videoEl.muted = !videoEl.muted;
        syncVolume();
        revealChrome();
    });
    volumeEl?.addEventListener("input", () => {
        if (!videoEl || !volumeEl) return;
        const value = Math.max(0, Math.min(100, Number(volumeEl.value || 0)));
        videoEl.volume = value / 100;
        videoEl.muted = value === 0;
        syncVolume();
        revealChrome();
    });
    progressEl?.addEventListener("pointerdown", () => {
        seekingWithPointer = true;
    });
    progressEl?.addEventListener("pointerup", () => {
        seekingWithPointer = false;
    });
    progressEl?.addEventListener("input", () => {
        if (!videoEl || !progressEl) return;
        videoEl.currentTime = Number(progressEl.value || 0);
        syncTimeline();
        revealChrome();
    });
    speedEl?.addEventListener("change", () => {
        if (!videoEl || !speedEl) return;
        videoEl.playbackRate = Number(speedEl.value || 1);
        revealChrome();
    });
    modalEl?.addEventListener("pointermove", revealChrome);
    videoEl?.addEventListener("click", togglePlayback);
    document.addEventListener("keydown", (event) => {
        if (!isOpen()) return;
        const target = event.target as HTMLElement | null;
        const tag = String(target?.tagName || "").toUpperCase();
        if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") return;
        if (event.code === "Space" || event.key === " ") {
            event.preventDefault();
            togglePlayback();
        } else if (event.key === "ArrowLeft") {
            event.preventDefault();
            seekBy(-SEEK_STEP_SECONDS);
        } else if (event.key === "ArrowRight") {
            event.preventDefault();
            seekBy(SEEK_STEP_SECONDS);
        } else if (event.key.toLowerCase() === "m") {
            event.preventDefault();
            if (activeNative?.token) {
                void nativeMediaCommand(activeNative.token, ["cycle", "mute"]).catch((err) => {
                    console.warn("NativeMediaCommand failed:", err);
                });
            } else {
                muteBtnEl?.click();
            }
        }
    });
    window.addEventListener("resize", scheduleNativeResize);
}

function renderSpeedOptions() {
    if (!speedEl) return;
    speedEl.innerHTML = RATE_OPTIONS.map((rate) => (
        `<option value="${rate}"${rate === 1 ? " selected" : ""}>${rate}x</option>`
    )).join("");
}

export function setupVideoModal() {
    modalEl = byID("video-modal");
    shellEl = byID("video-shell");
    filenameEl = byID("video-filename");
    metaEl = byID("video-meta");
    closeBtnEl = byID("video-close");
    nativeViewportEl = byID("video-native-viewport");
    videoEl = byID("video-player");
    loadingEl = byID("video-loading");
    errorEl = byID("video-error");
    playBtnEl = byID("video-play");
    muteBtnEl = byID("video-mute");
    progressEl = byID("video-progress");
    volumeEl = byID("video-volume");
    timeEl = byID("video-time");
    durationEl = byID("video-duration");
    speedEl = byID("video-speed");

    if (!modalEl || !videoEl) {
        console.error("Video modal setup failed. Missing #video-modal or #video-player.");
        return;
    }
    a11y = installModalA11y(modalEl, {
        requestClose: () => void closeVideoModal(),
        initialFocus: () => playBtnEl || closeBtnEl,
        restoreFocus: "#file-list",
    });
    renderSpeedOptions();
    bindVideoEvents();
    bindControls();
    syncTimeline();
    syncVolume();
}
