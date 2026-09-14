import { htmlPictureStyle, loadPlaybackPreferences, normalizePlaybackPreferences, savePlaybackPreferences, type PlaybackPreferences, type PictureMode } from "../video/playback-preferences";
import {
    attachNativeMedia,
    closeMedia,
    closeNativeMedia,
    getMediaStats,
    onRuntimeEvent,
    openMedia,
    openNativeMedia,
    updateMediaPlayback,
    type MediaStats,
    type MediaOpenResult,
    type NativeMediaOpenResult,
    type NativeMediaRect,
} from "../../api";

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
    shortNativeTrackLabel,
    type NativeMediaTrack,
} from "../video/media-tracks";
import {
    EMPTY_PLAYER_STATE,
    HtmlVideoAdapter,
    MAX_PLAYBACK_RATE,
    MIN_PLAYBACK_RATE,
    NativeMediaStateRouter,
    NativeMpvAdapter,
    clampPlaybackRate,
    type PlayerAdapter,
    type PlayerState,
} from "../video/player-adapters";
import { VideoGeometryController } from "../video/video-geometry";
import { SEEK_STEP_SECONDS, VOLUME_STEP, VideoTransportController } from "../video/video-transport";
import { bindVideoDOM, byID, collectVideoDOM, type VideoDOM } from "../video/video-dom";
import { activateModalOwnership, deactivateModalOwnership, installModalA11y } from "../../ui/modals/modal-a11y";
import { videoPlaybackPreferences } from "../../ui/video/video-preferences-store";
import { loadAutoNextPreference } from "../video/video-playlist";
import {
    resetVideoPlaylist,
    setVideoPlaylist,
    setVideoPlaylistAutoNext,
    setVideoPlaylistCurrentIndex,
    setVideoPlaylistOpen,
    setVideoPlaylistSwitching,
    type VideoPlaylistViewItem,
} from "../../ui/video/video-playlist-store";

const CHROME_HIDE_DELAY_MS = 2500;
const LOADING_DEBOUNCE_MS = 250;
const RATE_OPTIONS = [0.5, 0.75, 1, 1.25, 1.5, 2];
const PLAYBACK_HINT_INTERVAL_MS = 1000;
const MEDIA_STATS_POLL_MS = 1000;
const STREAM_ACTIVITY_HOLD_MS = 2000;

interface VideoOpenTarget {
    id: number;
    name: string;
    key?: string;
    size?: number;
    encrypted?: boolean;
}

export interface VideoPlaylistLaunch {
    readonly items: readonly VideoOpenTarget[];
    readonly currentIndex: number;
    readonly title: string;
}

interface ActiveVideoPlaylist {
    readonly items: readonly VideoOpenTarget[];
    readonly title: string;
    currentIndex: number;
    autoNext: boolean;
}

interface VideoOpenAttempt {
    generation: number;
    target: VideoOpenTarget;
    htmlFailureHandled: boolean;
    nativeFallbackRequested: boolean;
    pausedByUser: boolean;
    playbackIntent: PlaybackIntent | null;
}

let playbackPreferences = loadPlaybackPreferences();
let settingsSection: "picture" | "audio" | "subtitle" | "speed" | "shortcuts" | null = null;
let settingsReturnFocus: HTMLElement | null = null;
let playlistOpen = false;
let playlistReturnFocus: HTMLElement | null = null;
let activePlaylist: ActiveVideoPlaylist | null = null;
let modalEl: HTMLElement | null = null;
let stageEl: HTMLElement | null = null;
let topbarEl: HTMLElement | null = null;
let controlsEl: HTMLElement | null = null;
let filenameEl: HTMLElement | null = null;
let metaEl: HTMLElement | null = null;
let closeBtnEl: HTMLButtonElement | null = null;
let videoEl: HTMLVideoElement | null = null;
let loadingEl: HTMLElement | null = null;
let loadingStatusEl: HTMLElement | null = null;
let errorEl: HTMLElement | null = null;
let errorMessageEl: HTMLElement | null = null;
let errorRetryBtnEl: HTMLButtonElement | null = null;
let playBtnEl: HTMLButtonElement | null = null;
let speedBtnEl: HTMLButtonElement | null = null;
let speedMenuEl: HTMLElement | null = null;
let audioPicker: TrackPicker | null = null;
let subtitlePicker: TrackPicker | null = null;

let activeAdapter: PlayerAdapter | null = null;
let activeNative: NativeMediaOpenResult | null = null;
let activeMediaToken = "";
let activeMediaEncrypted = false;
let unsubscribeState: (() => void) | null = null;
let currentState: PlayerState = { ...EMPTY_PLAYER_STATE };
let activeOpenAttempt: VideoOpenAttempt | null = null;
const playbackTransitions = new SerialPlaybackTransitions();
const nativeStateRouter = new NativeMediaStateRouter();
let unsubscribeEncryptedMediaSessionsClosed: (() => void) | null = null;
let chromeHideTimer: ReturnType<typeof setTimeout> | null = null;
let loadingTimer: ReturnType<typeof setTimeout> | null = null;
let loadingStatusOverride = "";
let hasError = false;
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
let a11y: ReturnType<typeof installModalA11y> | null = null;
let videoHostObserver: MutationObserver | null = null;
let videoHostEl: HTMLElement | null = null;
let unbindVideoDOM: (() => void) | null = null;
let unbindSpeedMenu: (() => void) | null = null;
let videoDOM: VideoDOM | null = null;
let geometry: VideoGeometryController | null = null;
let transport: VideoTransportController | null = null;
let videoSetupComplete = false;

function isOpen() {
    return Boolean(modalEl && modalEl.style.display !== "none");
}

function errorDetail(err: unknown) {
    return err instanceof Error && err.message ? err.message : String(err || "");
}

function errorMessage(err: unknown, fallback: string) {
    const normalized = errorDetail(err).toLowerCase();
    if (
        normalized.includes("resolve peer") ||
        normalized.includes("rpcdorequest") ||
        normalized.includes("retryuntilack") ||
        normalized.includes("engine forcibly closed")
    ) {
        return "Could not reach Telegram. Check your connection and try again.";
    }
    if (normalized.includes("context canceled")) {
        return "The video request was canceled. Try again.";
    }
    return fallback;
}

function isNativeFallbackActive() {
    return Boolean(activeNative && !activeNative.htmlControls && activeNative.presentation !== "standalone");
}

function setChromeVisible(visible: boolean) {
    modalEl?.classList.toggle("is-video-chrome-visible", visible);
    modalEl?.classList.toggle("is-video-cursor-hidden", !visible && !hasError && !isNativeFallbackActive());
}


function clearChromeTimer() {
    if (!chromeHideTimer) return;
    clearTimeout(chromeHideTimer);
    chromeHideTimer = null;
}

function scheduleChromeHide() {
    clearChromeTimer();
    if (!isOpen() || currentState.paused || hasError || isAnyMenuOpen() || isScrubberTooltipActive()) return;
    chromeHideTimer = setTimeout(() => {
        if (!isOpen() || currentState.paused || hasError || isAnyMenuOpen() || isScrubberTooltipActive()) return;
        setChromeVisible(false);
    }, CHROME_HIDE_DELAY_MS);
}

function isScrubberTooltipActive() {
    return transport?.isScrubberTooltipActive() ?? false;
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


function setError(message: string) {
    hasError = true;
    loadingStatusOverride = "";
    setLoading(false);
    clearMediaStatsPolling();
    if (errorMessageEl) errorMessageEl.textContent = message;
    if (errorEl) errorEl.style.display = "block";
    modalEl?.classList.add("is-video-error");
    setChromeVisible(true);
    clearChromeTimer();
    if (isOpen()) errorRetryBtnEl?.focus({ preventScroll: true });
}

function clearError() {
    const restoreFocus = Boolean(errorEl?.contains(document.activeElement));
    hasError = false;
    errorMessageEl?.replaceChildren();
    if (errorEl) errorEl.style.display = "none";
    modalEl?.classList.remove("is-video-error");
    if (restoreFocus && isOpen()) (closeBtnEl || playBtnEl)?.focus({ preventScroll: true });
}

function retryVideoOpen() {
    const target = activeOpenAttempt?.target;
    if (!target || !hasError || !isOpen()) return;
    void openVideoTarget(target, null);
}

function handleHtmlPlaybackError(detail: string) {
    console.error("HTML video playback failed:", detail);
    setError("The embedded player could not continue playing this video. Try again.");
}


function syncSpeed(state: PlayerState) {
    const customInput = byID<HTMLInputElement>("video-speed-custom-input");
    if (speedBtnEl) {
        speedBtnEl.textContent = `${formatRate(state.rate)}x`;
        speedBtnEl.title = `Choose playback speed: ${formatRate(state.rate)}x`;
        speedBtnEl.setAttribute("aria-label", speedBtnEl.title);
    }
    if (customInput && document.activeElement !== customInput) {
        customInput.value = formatRate(state.rate);
    }
    const slider = byID<HTMLInputElement>("video-speed-slider");
    if (slider) {
        slider.value = String(state.rate);
        slider.setAttribute("aria-valuetext", `${formatRate(state.rate)} times`);
        slider.style.setProperty("--range-fill", `${(state.rate - MIN_PLAYBACK_RATE) / (MAX_PLAYBACK_RATE - MIN_PLAYBACK_RATE) * 100}%`);
    }
    const value = byID("video-speed-value");
    if (value) value.textContent = `${formatRate(state.rate)}x`;
    speedMenuEl?.querySelectorAll<HTMLButtonElement>("[data-rate]").forEach((button) => {
        const selected = Math.abs(Number(button.dataset.rate || 1) - state.rate) < 0.001;
        button.classList.toggle("is-selected", selected);
        button.setAttribute("aria-checked", selected ? "true" : "false");
    });
}

// Keep available audio and subtitle tracks discoverable, even with one track. A pill appearing changes the
// controls height, so the native viewport is re-measured.
function syncNativeTracks(tracks: NativeMediaTrack[]) {
    const available = Boolean(activeNative && activeNative.presentation !== "standalone");
    const audio = tracks.filter((track) => track.type === "audio");
    const subtitles = tracks.filter((track) => track.type === "subtitle");
    const selectedCodec = subtitles.find((track) => track.selected)?.codec?.toLowerCase() ?? "";
    const bitmapSubtitle = ["hdmv_pgs_subtitle", "pgs", "dvd_subtitle", "dvdsub", "dvb_subtitle", "dvbsub", "xsub"].includes(selectedCodec);
    const formatNote = byID("video-subtitle-format-note");
    if (formatNote) formatNote.hidden = !available || !bitmapSubtitle;
    const audioChanged = audioPicker?.update(available ? audio : [], available && audio.length > 0);
    const subtitleChanged = subtitlePicker?.update(available ? subtitles : [], available && subtitles.length > 0);
    if (audioChanged || subtitleChanged) geometry?.scheduleNativeResize();
}

interface TrackPickerElements {
    wrap: HTMLElement | null;
    button: HTMLButtonElement | null;
    label: HTMLElement | null;
    menu: HTMLElement | null;
}

// TrackPicker opens its full choices in the settings dock.
class TrackPicker {
    private tracks: NativeMediaTrack[] = [];
    private renderedSignature = "";

    constructor(
        private readonly title: string,
        private readonly offLabel: string | null,
        private readonly apply: (adapter: NativeMpvAdapter, id: number | null) => void,
        private readonly els: TrackPickerElements,
    ) {
        els.button?.addEventListener("click", (event) => {
            event.stopPropagation();
            this.setOpen(true);
            revealChrome();
        });
        els.menu?.addEventListener("click", (event) => {
            const item = (event.target as HTMLElement | null)?.closest<HTMLButtonElement>("[data-track]");
            if (!item) return;
            this.select(item.dataset.track === "no" ? null : Number(item.dataset.track));
            this.close(true);
        });
        els.menu?.addEventListener("keydown", (event) => {
            if (this.isOpen()) handleMenuKeydown(event, this.items().filter((item) => !item.hidden), () => this.close(true));
        });
    }

    get visible() {
        return Boolean(this.els.wrap && !this.els.wrap.hidden);
    }

    // update returns whether the pill appeared or disappeared.
    update(tracks: NativeMediaTrack[], show: boolean): boolean {
        const wasVisible = this.visible;
        this.tracks = tracks;
        if (this.els.wrap) this.els.wrap.hidden = !show;
        if (!show) {
            this.close();
            if (tracks.length === 0) {
                this.renderedSignature = "";
                this.els.menu?.replaceChildren();
                if (this.els.menu) this.els.menu.innerHTML = `<p class="video-settings-note">No ${this.title.toLowerCase()} tracks available.</p>`;
                return wasVisible;
            }
        }
        const signature = JSON.stringify(this.tracks.map((track) => [track.id, track.title, track.language, track.codec]));
        if (signature !== this.renderedSignature) {
            this.renderedSignature = signature;
            this.render();
        }
        this.syncSelection();
        return !wasVisible;
    }

    isOpen() {
        return Boolean(this.els.menu?.classList.contains("is-open"));
    }

    setOpen(open: boolean) {
        if (open) {
            closeMenus(this);
            showSettingsPanel(this.offLabel === null ? "audio" : "subtitle");
        }
        this.els.menu?.classList.toggle("is-open", open);
        if (open) {
            clearChromeTimer();
            requestAnimationFrame(() => { if (this.isOpen()) this.selectedItem()?.focus({ preventScroll: true }); });
        } else {
            if (settingsSection === (this.offLabel === null ? "audio" : "subtitle")) hideSettingsPanel();
            if (isOpen() && !currentState.paused && !hasError) scheduleChromeHide();
        }
    }

    close(restoreFocus = false) {
        if (!this.isOpen()) return;
        this.setOpen(false);
        if (restoreFocus) byID("video-picture-button")?.focus({ preventScroll: true });
    }

    contains(target: Node | null) {
        return Boolean(target && (this.els.menu?.contains(target) || this.els.button?.contains(target)));
    }

    // toggle switches an optional track (subtitles) between off and its default.
    toggle() {
        if (!this.visible || this.offLabel === null) return;
        const selected = this.tracks.find((track) => track.selected);
        this.select(selected ? null : this.defaultTrack()?.id ?? null);
    }

    private defaultTrack() {
        return this.tracks.find((track) => track.default) ?? this.tracks[0];
    }

    private currentTrack() {
        return this.tracks.find((track) => track.selected) ?? (this.offLabel === null ? this.defaultTrack() : undefined);
    }

    private select(id: number | null) {
        if (!(activeAdapter instanceof NativeMpvAdapter)) return;
        if (id !== null && !this.tracks.some((track) => track.id === id)) return;
        this.apply(activeAdapter, id);
        // Reflect the choice immediately; mpv confirms it on the next state event.
        this.tracks = this.tracks.map((track) => ({ ...track, selected: track.id === id }));
        this.syncSelection();
        revealChrome();
    }


    private items() {
        return Array.from(this.els.menu?.querySelectorAll<HTMLButtonElement>("[data-track]") || []);
    }

    private selectedItem() {
        return this.items().find((item) => item.classList.contains("is-selected")) || this.items()[0] || null;
    }

    private render() {
        if (!this.els.menu) return;
        const items = this.tracks.map((track, index) => menuItemMarkup(`data-track="${track.id}"`, nativeTrackLabel(track, index)));
        if (this.offLabel !== null) items.unshift(menuItemMarkup('data-track="no"', this.offLabel));
        this.els.menu.innerHTML = `<div class="video-menu-title">${this.title}</div><div class="video-track-list" role="radiogroup" aria-label="${this.title} tracks">${items.join("")}</div>`;
    }

    private syncSelection() {
        const current = this.currentTrack();
        const key = current ? String(current.id) : "no";
        for (const item of this.items()) {
            const on = item.dataset.track === key;
            item.classList.toggle("is-selected", on);
            item.setAttribute("aria-checked", on ? "true" : "false");
            item.tabIndex = on ? 0 : -1;
        }
        const index = current ? this.tracks.indexOf(current) : -1;
        const short = current ? shortNativeTrackLabel(current, index) : this.offLabel ?? "";
        const full = current ? nativeTrackLabel(current, index) : this.offLabel ?? "";
        if (this.els.label) this.els.label.textContent = short;
        if (this.els.button) {
            this.els.button.dataset.state = current ? "on" : "off";
            this.els.button.title = `Choose ${this.title.toLowerCase()}: ${full}`;
            this.els.button.setAttribute("aria-label", this.els.button.title);
        }
    }
}

export function updatePlaybackPreferences(value: PlaybackPreferences): void {
    playbackPreferences = normalizePlaybackPreferences(value);
    videoPlaybackPreferences.set(playbackPreferences);
    savePlaybackPreferences(playbackPreferences);
    syncAspectButton();
    if (activeAdapter instanceof NativeMpvAdapter) activeAdapter.applyPreferences(playbackPreferences);
    applyHtmlPicture();
}

const PICTURE_MODES: PictureMode[] = ["fit", "fill", "original", "16:9", "4:3"];
const PICTURE_LABELS: Record<PictureMode, string> = { fit: "Fit", fill: "Fill", original: "Original", "16:9": "16:9", "4:3": "4:3" };

function syncAspectButton() {
    const button = byID("video-aspect-button");
    if (!button) return;
    const label = PICTURE_LABELS[playbackPreferences.pictureMode];
    button.textContent = label;
    button.title = `Video fit: ${label}. Click to cycle`;
    button.setAttribute("aria-label", button.title);
}

function applyHtmlPicture() {
    if (!videoEl || !stageEl) return;
    const rect = stageEl.getBoundingClientRect();
    const style = htmlPictureStyle(playbackPreferences.pictureMode, rect.width, rect.height, videoEl.videoWidth, videoEl.videoHeight);
    Object.assign(videoEl.style, style);
}

function getActiveVideoPanel(): HTMLElement | null {
    if (settingsSection !== null) return byID("video-settings-panel");
    if (playlistOpen) return byID("video-playlist-panel");
    return null;
}

function syncActivePanelGeometry() {
    const panel = getActiveVideoPanel();
    if (!panel || !modalEl) return;
    const shell = byID("video-shell")?.getBoundingClientRect();
    if (!shell) return;
    const top = Math.max(0, (topbarEl?.getBoundingClientRect().bottom ?? shell.top) - shell.top);
    const bottom = Math.max(0, shell.bottom - (controlsEl?.getBoundingClientRect().top ?? shell.bottom));
    panel.style.setProperty("--video-panel-top", `${top}px`);
    panel.style.setProperty("--video-panel-bottom", `${bottom}px`);
    geometry?.syncFallbackNativeViewportInsets();
}

function showSettingsPanel(section: NonNullable<typeof settingsSection>) {
    const panel = byID("video-settings-panel");
    if (!panel) return;
    settingsReturnFocus = byID("video-picture-button");
    hideVideoPlaylist();
    settingsSection = section;
    panel.hidden = false;
    panel.setAttribute("aria-hidden", "false");
    panel.inert = false;
    modalEl?.classList.add("has-video-settings");
    byID("video-picture-button")?.setAttribute("aria-expanded", "true");
    const picture = byID("video-picture-settings");
    const appearance = byID("video-subtitle-settings");
    const shortcuts = byID("video-shortcuts-settings");
    if (picture) picture.hidden = section !== "picture";
    if (appearance) appearance.hidden = section !== "subtitle";
    if (shortcuts) shortcuts.hidden = section !== "shortcuts";
    for (const button of panel.querySelectorAll<HTMLElement>("[data-settings-section]")) {
        button.setAttribute("aria-pressed", button.dataset.settingsSection === section ? "true" : "false");
    }
    syncActivePanelGeometry();
    clearChromeTimer();
    revealChrome();
    requestAnimationFrame(() => { syncActivePanelGeometry(); geometry?.scheduleNativeResize(); applyHtmlPicture(); });
}

function hideSettingsPanel(restoreFocus = false) {
    settingsSection = null;
    const panel = byID("video-settings-panel");
    const focusInside = Boolean(panel?.contains(document.activeElement));
    if (panel) {
        panel.hidden = true;
        panel.setAttribute("aria-hidden", "true");
        panel.inert = true;
    }
    modalEl?.classList.remove("has-video-settings");
    byID("video-picture-button")?.setAttribute("aria-expanded", "false");
    if ((restoreFocus || focusInside) && settingsReturnFocus?.isConnected) settingsReturnFocus.focus({ preventScroll: true });
    geometry?.syncFallbackNativeViewportInsets();
    geometry?.scheduleNativeResize();
    applyHtmlPicture();
}

function showVideoPlaylist() {
    if (!activePlaylist || activePlaylist.items.length < 2) return;
    closeMenus();
    const panel = byID("video-playlist-panel");
    if (!panel) return;
    playlistReturnFocus = byID("video-playlist-button");
    playlistOpen = true;
    panel.hidden = false;
    panel.inert = false;
    panel.setAttribute("aria-hidden", "false");
    modalEl?.classList.add("has-video-playlist");
    byID("video-playlist-button")?.setAttribute("aria-expanded", "true");
    setVideoPlaylistOpen(true);
    syncActivePanelGeometry();
    clearChromeTimer();
    revealChrome();
    requestAnimationFrame(() => {
        syncActivePanelGeometry();
        geometry?.scheduleNativeResize();
        applyHtmlPicture();
        panel.querySelector<HTMLElement>("[aria-current='true']")?.focus({ preventScroll: true });
    });
}

export function hideVideoPlaylist(restoreFocus = false): void {
    const panel = byID("video-playlist-panel");
    const focusInside = Boolean(panel?.contains(document.activeElement));
    playlistOpen = false;
    if (panel) {
        panel.hidden = true;
        panel.inert = true;
        panel.setAttribute("aria-hidden", "true");
    }
    modalEl?.classList.remove("has-video-playlist");
    byID("video-playlist-button")?.setAttribute("aria-expanded", "false");
    setVideoPlaylistOpen(false);
    if ((restoreFocus || focusInside) && playlistReturnFocus?.isConnected) {
        playlistReturnFocus.focus({ preventScroll: true });
    }
    geometry?.syncFallbackNativeViewportInsets();
    geometry?.scheduleNativeResize();
    applyHtmlPicture();
}

function bindVideoPlaylist() {
    byID("video-playlist-button")?.addEventListener("click", (event) => {
        event.stopPropagation();
        if (playlistOpen) hideVideoPlaylist(true);
        else showVideoPlaylist();
    });
}

function bindSettingsPanel() {
    const panel = byID("video-settings-panel");
    byID("video-picture-button")?.addEventListener("click", (event) => {
        event.stopPropagation();
        const wasOpen = settingsSection !== null;
        closeMenus();
        if (!wasOpen) {
            showSettingsPanel("picture");
            panel?.querySelector<HTMLButtonElement>("[data-picture-mode]")?.focus();
        }
    });
    byID("video-aspect-button")?.addEventListener("click", (event) => {
        event.stopPropagation();
        const next = PICTURE_MODES[(PICTURE_MODES.indexOf(playbackPreferences.pictureMode) + 1) % PICTURE_MODES.length];
        updatePlaybackPreferences({ ...playbackPreferences, pictureMode: next });
        revealChrome();
    });
    syncAspectButton();
    byID("video-settings-close")?.addEventListener("click", () => closeOpenMenu());
    panel?.addEventListener("click", (event) => {
        const button = (event.target as HTMLElement).closest<HTMLElement>("[data-settings-section]");
        if (!button) return;
        const returnFocus = settingsReturnFocus;
        closeMenus();
        const section = button.dataset.settingsSection;
        if (section === "audio") audioPicker?.setOpen(true);
        else if (section === "subtitle") subtitlePicker?.setOpen(true);
        else if (section === "speed") setSpeedMenuOpen(true);
        else if (section === "shortcuts") showSettingsPanel("shortcuts");
        else showSettingsPanel("picture");
        settingsReturnFocus = returnFocus;
    });
    panel?.addEventListener("keydown", (event) => {
        if (event.key !== "Escape") return;
        event.preventDefault();
        event.stopPropagation();
        closeOpenMenu();
    });
}

function trackPickers() {
    return [audioPicker, subtitlePicker].filter((picker): picker is TrackPicker => picker !== null);
}

function isAnyMenuOpen() {
    return playlistOpen || settingsSection !== null || isSpeedMenuOpen() || trackPickers().some((picker) => picker.isOpen());
}

// closeMenus closes every popover except the one about to open.
function closeMenus(except: TrackPicker | "speed" | null = null) {
    if (settingsSection !== null) hideSettingsPanel();
    if (except !== "speed") closeSpeedMenu();
    for (const picker of trackPickers()) {
        if (picker !== except) picker.close();
    }
}

// closeOpenMenu closes whichever popover or panel is open, returning whether one was.
function closeOpenMenu() {
    if (playlistOpen) {
        hideVideoPlaylist(true);
        return true;
    }
    if (!isAnyMenuOpen()) return false;
    closeSpeedMenu(true);
    for (const picker of trackPickers()) picker.close(true);
    hideSettingsPanel(true);
    return true;
}

function menuItemMarkup(attributes: string, label: string) {
    return `<button type="button" role="radio" ${attributes} aria-checked="false"><span class="video-menu-check" aria-hidden="true">✓</span><span>${escapeHTML(label)}</span></button>`;
}

function escapeHTML(value: string) {
    return value.replace(/[&<>"']/g, (char) => `&#${char.charCodeAt(0)};`);
}

function handleMenuKeydown(event: KeyboardEvent, buttons: HTMLButtonElement[], close: () => void) {
    if ((event.target as HTMLElement)?.tagName === "INPUT" && event.key === "ArrowDown") {
        buttons[0]?.focus();
        event.preventDefault();
        event.stopPropagation();
        return;
    }
    const current = Math.max(0, buttons.indexOf(document.activeElement as HTMLButtonElement));
    const focusAt = (index: number) => buttons[(index + buttons.length) % buttons.length]?.focus({ preventScroll: true });
    switch (event.key) {
        case "Escape":
            close();
            break;
        case "ArrowDown":
        case "ArrowRight":
            focusAt(current + 1);
            break;
        case "ArrowUp":
        case "ArrowLeft":
            focusAt(current - 1);
            break;
        case "Home":
            focusAt(0);
            break;
        case "End":
            focusAt(buttons.length - 1);
            break;
        case "Enter":
        case " ":
            (document.activeElement as HTMLButtonElement | null)?.click();
            break;
        default:
            return;
    }
    event.preventDefault();
    event.stopPropagation();
}

function parseCustomPlaybackRate(value: string) {
    const rate = Number(value.trim());
    if (!Number.isFinite(rate) || rate <= 0) return null;
    return clampPlaybackRate(rate);
}

function applyState(state: PlayerState) {
    const wasPaused = currentState.paused;
    currentState = state;
    if (!state.loading && loadingStatusOverride) {
        loadingStatusOverride = "";
    }
    schedulePlaybackHint(state);
    transport?.sync(state);
    syncSpeed(state);
    syncNativeTracks(state.tracks);
    applyHtmlPicture();
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
// the HTML <video> and native mpv engines when sending backend playback hints.
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
    if (adapter instanceof NativeMpvAdapter) nativeStateRouter.deactivate(adapter);
    activeAdapter = null;
    activeNative = null;
    activeMediaToken = "";
    activeMediaEncrypted = false;
    unsubscribeState?.();
    unsubscribeState = null;
    clearPlaybackHintTimer();
    clearMediaStatsPolling();
    transport?.resetSession();
    closeMenus();
    hideSettingsPanel();
    geometry?.setNativeLayout("none");
    if (nativeToken) nativeStateRouter.discard(nativeToken);
    currentState = { ...EMPTY_PLAYER_STATE };
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
        nativeStateRouter.discard(token);
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

function normalizeVideoTarget(target: VideoOpenTarget): VideoOpenTarget | null {
    const id = Number(target.id || 0);
    if (!Number.isFinite(id) || id <= 0) return null;
    const normalized = {
        id,
        name: String(target.name || "Video"),
        size: Math.max(0, Number(target.size) || 0),
        encrypted: Boolean(target.encrypted),
    };
    const key = String(target.key || "").trim();
    return key ? { ...normalized, key } : normalized;
}

function createActivePlaylist(target: VideoOpenTarget, launch?: VideoPlaylistLaunch): ActiveVideoPlaylist {
    const fallback = Object.freeze([{ ...target }]);
    if (!launch) {
        return { items: fallback, title: "Videos", currentIndex: 0, autoNext: loadAutoNextPreference() };
    }

    const items = launch.items
        .map(normalizeVideoTarget)
        .filter((item): item is VideoOpenTarget => item !== null);
    let currentIndex = Number.isInteger(launch.currentIndex) ? launch.currentIndex : -1;
    if (currentIndex < 0 || currentIndex >= items.length || items[currentIndex].id !== target.id) {
        const matches = items.reduce<number[]>((indices, item, index) => {
            if (item.id === target.id) indices.push(index);
            return indices;
        }, []);
        currentIndex = matches.length === 1 ? matches[0] : -1;
    }
    if (currentIndex < 0) {
        return { items: fallback, title: "Videos", currentIndex: 0, autoNext: loadAutoNextPreference() };
    }
    items[currentIndex] = { ...items[currentIndex], ...target };
    return {
        items: Object.freeze(items.map((item) => Object.freeze({ ...item }))),
        title: String(launch.title || "Videos"),
        currentIndex,
        autoNext: loadAutoNextPreference(),
    };
}

function playlistItemIdentity(item: VideoOpenTarget, index: number): string {
    return item.key || `video:${item.id}:${index}`;
}

function playlistViewItems(playlist: ActiveVideoPlaylist): readonly VideoPlaylistViewItem[] {
    return playlist.items.map((item, index) => ({
        id: playlistItemIdentity(item, index),
        name: item.name,
        size: item.size || 0,
        format: videoFormatLabel(item.name),
        position: index + 1,
    }));
}

function syncPlaylistButton() {
    const button = byID<HTMLButtonElement>("video-playlist-button");
    const count = activePlaylist?.items.length ?? 0;
    const available = count > 1;
    if (!button) return;
    button.hidden = !available;
    const position = available ? activePlaylist!.currentIndex + 1 : 0;
    button.setAttribute("aria-label", available ? `Playlist, ${position} of ${count}` : "Playlist");
    button.title = available ? `Playlist (${position} of ${count})` : "Playlist";
}

function syncPlaylistSnapshot() {
    if (!activePlaylist) {
        resetVideoPlaylist();
        syncPlaylistButton();
        return;
    }
    setVideoPlaylist({
        open: playlistOpen && activePlaylist.items.length > 1,
        title: activePlaylist.title,
        items: playlistViewItems(activePlaylist),
        currentIndex: activePlaylist.currentIndex,
        autoNext: activePlaylist.autoNext,
        switchingId: null,
    });
    syncPlaylistButton();
}

function installVideoPlaylist(target: VideoOpenTarget, launch?: VideoPlaylistLaunch) {
    hideVideoPlaylist();
    activePlaylist = createActivePlaylist(target, launch);
    syncPlaylistSnapshot();
}

function nextPlaylistPlaybackIntent(): PlaybackIntent {
    const intent = capturePlaybackIntent(currentState, false);
    intent.currentTime = 0;
    intent.paused = false;
    return intent;
}

async function switchVideoPlaylistItem(index: number, closePanel: boolean): Promise<void> {
    const playlist = activePlaylist;
    if (!playlist || index < 0 || index >= playlist.items.length) return;
    if (index === playlist.currentIndex) {
        if (closePanel) hideVideoPlaylist(true);
        return;
    }
    const target = playlist.items[index];
    const intent = nextPlaylistPlaybackIntent();
    playlist.currentIndex = index;
    setVideoPlaylistCurrentIndex(index);
    setVideoPlaylistSwitching(playlistItemIdentity(target, index));
    syncPlaylistButton();
    if (closePanel) hideVideoPlaylist(true);
    try {
        await openVideoTarget(target, intent);
    } finally {
        if (activePlaylist === playlist && playlist.currentIndex === index) {
            setVideoPlaylistSwitching(null);
        }
    }
}

export function selectVideoPlaylistItem(index: number): void {
    void switchVideoPlaylistItem(index, true);
}

export function updateVideoAutoNext(enabled: boolean): void {
    if (activePlaylist) activePlaylist.autoNext = Boolean(enabled);
    setVideoPlaylistAutoNext(Boolean(enabled));
}

function handleNaturalMediaEnd(attempt: VideoOpenAttempt, adapter: PlayerAdapter) {
    if (
        activeOpenAttempt !== attempt ||
        activeAdapter !== adapter ||
        !playbackTransitions.isCurrent(attempt.generation) ||
        !activePlaylist?.autoNext
    ) {
        return;
    }
    const nextIndex = activePlaylist.currentIndex + 1;
    if (nextIndex >= activePlaylist.items.length) {
        setChromeVisible(true);
        clearChromeTimer();
        return;
    }
    void switchVideoPlaylistItem(nextIndex, false);
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
        transport?.beginSession(opened.thumbnailUrl);
        activeMediaToken = opened.token;
        activeMediaEncrypted = Boolean(opened.info.encrypted);

        adapter = new HtmlVideoAdapter(videoEl!, opened, {
            mediaError: (code, state) => handleHtmlMediaError(attempt, adapter!, code, state),
            playbackError: handleHtmlPlaybackError,
            revealChrome,
            mediaEnded: () => handleNaturalMediaEnd(attempt, adapter!),
        });
        activeAdapter = adapter;
        unsubscribeState = adapter.subscribe((state) => {
            if (!isCurrent() || activeAdapter !== adapter) return;
            applyState(state);
        });
        adapter.load();
        if (attempt.playbackIntent) {
            adapter.setVolume(attempt.playbackIntent.volume);
            adapter.setMuted(attempt.playbackIntent.muted);
            adapter.setSpeed(attempt.playbackIntent.rate);
        }
    } catch (err: unknown) {
        if (adapter && activeAdapter === adapter) {
            await releaseActive();
        } else if (opened?.token) {
            if (activeMediaToken === opened.token) detachActiveReferences();
            await safelyCloseMedia(opened.token);
        }
        if (!isCurrent()) return;
        console.error("OpenMedia failed:", err);
        setError(errorMessage(err, "Could not open this video. Try again."));
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
    console.error("HTML media player failed:", { code, state });

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
        : "The embedded player could not continue playing this video. Try again.";
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
        const rect = await geometry?.prepareNativeRect(isCurrent);
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
        geometry?.setNativeLayout("none");
        // A closed loopback session also surfaces as an HTML media error, so the
        // handoff can race a dead token; report that as the interruption it is.
        const sessionLost = Boolean(existing) && /session not found/i.test(errorDetail(err));
        setError(sessionLost
            ? "The video stream was interrupted. Check your connection and try again."
            : errorMessage(err, existing
                ? "The compatible player could not open this video. Try again."
                : "Could not open this video. Try again."));
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
            setError("The compatible player stopped unexpectedly. Try again.");
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
    geometry?.setNativeLayout(standalone ? "standalone" : opened.htmlControls ? "embedded-overlay" : "embedded-fallback");
    activeNative = opened;
    activeMediaToken = opened.token;
    activeMediaEncrypted = Boolean(opened.info.encrypted);
    const displayName = opened.info.name || opened.name || attempt.target.name || "Video";
    const displaySize = opened.info.plaintextSize || opened.info.storedSize || attempt.target.size || 0;
    updateMediaText(displayName, displaySize);
    transport?.beginSession(opened.thumbnailUrl);

    const adapter = new NativeMpvAdapter(opened, {
        mediaError: (detail) => handleNativeMediaError(attempt, opened.token, detail),
        mediaClosed: () => handleNativeMediaClosed(attempt, opened.token),
        mediaEnded: () => handleNaturalMediaEnd(attempt, adapter),
        dispose: (disposed) => nativeStateRouter.deactivate(disposed),
    });
    activeAdapter = adapter;
    if (nativeStateRouter.activate(adapter)) return;
    adapter.applyPreferences(playbackPreferences);
    if (intent) {
        adapter.setVolume(intent.volume);
        adapter.setMuted(intent.muted);
        adapter.setSpeed(intent.rate);
        adapter.setPaused(intent.paused);
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
    if (!standalone) geometry?.scheduleNativeResize();
}

async function openVideoTarget(target: VideoOpenTarget, playbackIntent: PlaybackIntent | null): Promise<void> {
    const host = byID<HTMLElement>("video-modal");
    if (videoHostEl !== host || !videoSetupComplete) activateVideoModal();
    if (!modalEl || !videoEl || !filenameEl || !metaEl) return;

    const attempt: VideoOpenAttempt = {
        generation: playbackTransitions.begin(),
        target,
        htmlFailureHandled: false,
        nativeFallbackRequested: false,
        pausedByUser: false,
        playbackIntent,
    };
    activeOpenAttempt = attempt;

    updateMediaText(target.name || "Video", target.size || 0);
    clearError();
    setLoadingStatusOverride("");
    setLoading(true);
    setChromeVisible(true);
    modalEl.style.display = "flex";
    modalEl.setAttribute("aria-hidden", "false");
    activateModalOwnership(modalEl);
    a11y?.activate();
    void geometry?.syncFullscreenState();

    await playbackTransitions.run(attempt.generation, async (isCurrent) => {
        await releaseActive();
        if (!isCurrent() || !isOpen()) return;
        setLoadingStatusOverride("");
        setLoading(true);
        if (isWebviewDirectVideo(attempt.target.name)) {
            await openHtmlPlayback(attempt, isCurrent);
            return;
        }
        const rect = await geometry?.prepareNativeRect(isCurrent);
        if (!rect || !isCurrent()) return;
        await openNativePlayback(attempt, rect, isCurrent, null, attempt.playbackIntent);
    });
}

export async function openVideoModal(target: VideoOpenTarget, playlist?: VideoPlaylistLaunch): Promise<void> {
    const normalized = normalizeVideoTarget(target);
    if (!normalized) return;
    const host = byID<HTMLElement>("video-modal");
    if (videoHostEl !== host || !videoSetupComplete) activateVideoModal();
    if (!modalEl || !videoEl || !filenameEl || !metaEl) return;
    installVideoPlaylist(normalized, playlist);
    await openVideoTarget(normalized, null);
}

export async function closeVideoModal() {
    if (!modalEl) return;
    const generation = playbackTransitions.begin();
    activeOpenAttempt = null;
    hideVideoPlaylist();
    activePlaylist = null;
    resetVideoPlaylist();
    clearChromeTimer();
    await geometry?.exitVideoFullscreen();
    if (!playbackTransitions.isCurrent(generation)) return;
    modalEl.style.display = "none";
    modalEl.setAttribute("aria-hidden", "true");
    deactivateModalOwnership(modalEl);
    a11y?.deactivate();
    await playbackTransitions.run(generation, async () => releaseActive());
    if (!playbackTransitions.isCurrent(generation)) return;
    clearError();
    setLoadingStatusOverride("");
    setLoading(false);
}


function isSpeedMenuOpen() {
    return Boolean(speedMenuEl?.classList.contains("is-open"));
}

function speedMenuButtons() {
    return Array.from(speedMenuEl?.querySelectorAll<HTMLButtonElement>("[data-rate]") || []);
}

function setSpeedMenuOpen(open: boolean) {
    if (open) {
        closeMenus("speed");
        showSettingsPanel("speed");
    }
    speedMenuEl?.classList.toggle("is-open", open);
    if (!open && settingsSection === "speed") hideSettingsPanel();
    if (open) {
        clearChromeTimer();
        requestAnimationFrame(() => {
            if (isSpeedMenuOpen() && !speedMenuEl?.contains(document.activeElement)) {
                byID("video-speed-slider")?.focus({ preventScroll: true });
            }
        });
    } else if (isOpen() && !currentState.paused && !hasError) {
        scheduleChromeHide();
    }
}

function closeSpeedMenu(restoreFocus = false) {
    if (!isSpeedMenuOpen()) return;
    setSpeedMenuOpen(false);
    if (restoreFocus) byID("video-picture-button")?.focus({ preventScroll: true });
}
function bindSpeedMenu() {
    speedBtnEl?.addEventListener("click", (event) => {
        event.stopPropagation();
        setSpeedMenuOpen(true);
        revealChrome();
    });
    speedMenuEl?.addEventListener("click", (event) => {
        const button = (event.target as HTMLElement | null)?.closest<HTMLButtonElement>("[data-rate]");
        if (!button || !activeAdapter) return;
        activeAdapter.setSpeed(Number(button.dataset.rate || 1));
        closeSpeedMenu(true);
        revealChrome();
    });
    speedMenuEl?.addEventListener("input", (event) => {
        const input = event.target;
        if (!(input instanceof HTMLInputElement) || input.id !== "video-speed-slider") return;
        const rate = parseCustomPlaybackRate(input.value);
        if (rate !== null) activeAdapter?.setSpeed(rate);
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
        if (target?.closest(".video-speed-custom, .video-speed-adjustment")) {
            if (event.key === "Escape") {
                event.preventDefault();
                event.stopPropagation();
                closeSpeedMenu(true);
            }
            return;
        }
        handleMenuKeydown(event, speedMenuButtons(), () => closeSpeedMenu(true));
    });
    const onDocumentClick = (event: MouseEvent) => {
        const target = event.target as Node | null;
        if (target && byID("video-settings-panel")?.contains(target)) return;
        if (isSpeedMenuOpen() && !(target && (speedMenuEl?.contains(target) || speedBtnEl?.contains(target)))) {
            closeSpeedMenu();
        }
        for (const picker of trackPickers()) {
            if (picker.isOpen() && !picker.contains(target)) picker.close();
        }
    };
    document.addEventListener("click", onDocumentClick);
    return () => document.removeEventListener("click", onDocumentClick);
}

function targetShouldUseOwnKeyboard(target: HTMLElement | null, event: KeyboardEvent) {
    if (!(target instanceof HTMLElement)) return false;
    if (target.isContentEditable || target.closest("#video-settings-panel, #video-playlist-panel")) return true;
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
    const target = event.target instanceof HTMLElement ? event.target : null;
    if (targetShouldUseOwnKeyboard(target, event)) return;
    const key = event.key.toLowerCase();
    if (event.code === "Space" || event.key === " " || key === "k") {
        event.preventDefault();
        transport?.togglePlayback();
    } else if (event.key === "ArrowLeft" || key === "j") {
        event.preventDefault();
        transport?.seekBy(-SEEK_STEP_SECONDS);
    } else if (event.key === "ArrowRight" || key === "l") {
        event.preventDefault();
        transport?.seekBy(SEEK_STEP_SECONDS);
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
        void geometry?.toggleFullscreen();
    } else if (key === "c") {
        event.preventDefault();
        subtitlePicker?.toggle();
    }
}

function handleVideoPointerMove() {
    revealChrome();
}

function targetIsVideoChrome(target: EventTarget | null) {
    const el = target instanceof Element ? target : null;
    return Boolean(el?.closest(".video-topbar, .video-controls, .video-center-controls, .video-error, .video-loading, .video-settings-panel, .video-playlist-panel"));
}

function handleStageClick(event: MouseEvent) {
    if (activeNative?.presentation === "standalone") return;
    if (targetIsVideoChrome(event.target)) return;
    transport?.togglePlayback();
}

function handleStageDoubleClick(event: MouseEvent) {
    if (activeNative?.presentation === "standalone") return;
    if (targetIsVideoChrome(event.target)) return;
    event.preventDefault();
    void geometry?.toggleFullscreen();
}

function bindEncryptedMediaLifecycle() {
    if (unsubscribeEncryptedMediaSessionsClosed) return;
    unsubscribeEncryptedMediaSessionsClosed = onRuntimeEvent("encrypted_media_sessions_closed", () => {
        if (!activeOpenAttempt || (!activeOpenAttempt.target.encrypted && !activeMediaEncrypted)) return;
        void closeVideoModal();
    });
}

function renderSpeedOptions() {
    if (!speedMenuEl) return;
    const options = RATE_OPTIONS.map(speedOptionMarkup).join("");
    speedMenuEl.innerHTML = `<div class="video-speed-adjustment"><label class="video-settings-field" for="video-speed-slider"><span>Playback speed <output id="video-speed-value">1x</output></span><input id="video-speed-slider" class="video-settings-range" type="range" min="${MIN_PLAYBACK_RATE}" max="${MAX_PLAYBACK_RATE}" step="0.05" value="1" aria-valuetext="1 times" /></label><div class="video-range-endpoints" aria-hidden="true"><span>${MIN_PLAYBACK_RATE}x</span><span>${MAX_PLAYBACK_RATE}x</span></div></div><div class="video-speed-presets" role="menu" aria-label="Speed presets">${options}</div>${customSpeedMarkup()}`;
}

function speedOptionMarkup(rate: number) {
    return `<button type="button" role="menuitemradio" data-rate="${rate}" aria-checked="${rate === 1 ? "true" : "false"}"><span class="video-menu-check" aria-hidden="true">✓</span><span>${formatRate(rate)}x</span></button>`;
}

function customSpeedMarkup() {
    return `<form class="video-speed-custom" role="none" aria-label="Custom playback speed"><label for="video-speed-custom-input">Custom</label><div class="video-speed-custom-row"><input id="video-speed-custom-input" type="number" inputmode="decimal" min="${MIN_PLAYBACK_RATE}" max="${MAX_PLAYBACK_RATE}" step="0.05" value="1" aria-label="Custom playback speed" /><span aria-hidden="true">x</span><button type="submit">Set</button></div></form>`;
}


export function teardownVideoModal(): void {
    playbackTransitions.begin();
    activeOpenAttempt = null;
    hideVideoPlaylist();
    activePlaylist = null;
    resetVideoPlaylist();
    clearChromeTimer();
    if (modalEl) {
        modalEl.style.display = "none";
        modalEl.setAttribute("aria-hidden", "true");
    }

    if (modalEl) deactivateModalOwnership(modalEl);
    a11y?.deactivate();
    a11y = null;
    unbindVideoDOM?.();
    unbindVideoDOM = null;
    unbindSpeedMenu?.();
    unbindSpeedMenu = null;
    nativeStateRouter.unbind();
    unsubscribeEncryptedMediaSessionsClosed?.();
    unsubscribeEncryptedMediaSessionsClosed = null;
    void releaseActive();
    transport?.destroy();
    geometry?.destroy();
    transport = null;
    geometry = null;
    videoDOM = null;
    audioPicker = null;
    subtitlePicker = null;

    videoHostObserver?.disconnect();
    videoHostObserver = null;

    modalEl = null;
    stageEl = null;
    topbarEl = null;
    controlsEl = null;
    filenameEl = null;
    metaEl = null;
    closeBtnEl = null;
    videoEl = null;
    loadingEl = null;
    loadingStatusEl = null;
    errorEl = null;
    errorMessageEl = null;
    errorRetryBtnEl = null;
    playBtnEl = null;
    speedBtnEl = null;
    speedMenuEl = null;
    settingsSection = null;
    settingsReturnFocus = null;
    playlistOpen = false;
    playlistReturnFocus = null;
    videoHostEl = null;
    videoSetupComplete = false;
}

export function activateVideoModal(): () => void {
    const host = byID<HTMLElement>("video-modal");
    if (!host) {
        if (videoSetupComplete || videoHostEl) teardownVideoModal();
        return () => {};
    }
    if (videoSetupComplete && videoHostEl === host && videoDOM?.modal === host) return teardownVideoModal;
    if (videoSetupComplete || videoHostEl) teardownVideoModal();

    videoHostEl = host;
    videoHostObserver = new MutationObserver(() => {
        if (!host.isConnected) teardownVideoModal();
    });
    videoHostObserver.observe(document.body, { childList: true, subtree: true });

    videoDOM = collectVideoDOM();
    ({
        modal: modalEl,
        stage: stageEl,
        topbar: topbarEl,
        controls: controlsEl,
        filename: filenameEl,
        meta: metaEl,
        closeButton: closeBtnEl,
        video: videoEl,
        loading: loadingEl,
        loadingStatus: loadingStatusEl,
        error: errorEl,
        errorMessage: errorMessageEl,
        errorRetryButton: errorRetryBtnEl,
        playButton: playBtnEl,
        speedButton: speedBtnEl,
        speedMenu: speedMenuEl,
    } = videoDOM);
    audioPicker = new TrackPicker("Audio", null, (adapter, id) => {
        if (id !== null) adapter.setAudioTrack(id);
    }, videoDOM.audioPicker);
    subtitlePicker = new TrackPicker(
        "Subtitles",
        "Off",
        (adapter, id) => adapter.setSubtitleTrack(id),
        videoDOM.subtitlePicker,
    );

    if (!modalEl || !videoEl || !stageEl) {
        console.error("Video modal setup failed. Missing #video-modal, #video-stage, or #video-player.");
        teardownVideoModal();
        return () => {};
    }

    geometry = new VideoGeometryController({
        dom: videoDOM,
        getNative: () => activeNative,
        hasActivePlayer: () => Boolean(activeAdapter),
        hasError: () => hasError,
        isOpen,
        getActivePanel: getActiveVideoPanel,
        revealChrome,
        reportSurfaceError: () => setError("Could not prepare the video player. Try again."),
    });
    transport = new VideoTransportController({
        dom: videoDOM,
        getAdapter: () => activeAdapter,
        getState: () => currentState,
        getNative: () => activeNative,
        hasError: () => hasError,
        isNativeFallbackActive,
        markPausedByUser: (paused) => {
            if (activeOpenAttempt) activeOpenAttempt.pausedByUser = paused;
        },
        revealChrome,
        scheduleChromeHide,
        geometryChanged: () => {
            syncActivePanelGeometry();
            geometry?.scheduleNativeResize();
        },
        refreshFullscreenAvailability: () => geometry?.refreshFullscreenAvailability(),
    });

    videoSetupComplete = true;
    a11y = installModalA11y(modalEl, {
        requestClose: () => {
            if (playlistOpen) {
                hideVideoPlaylist(true);
                return;
            }
            const panel = byID<HTMLElement>("video-settings-panel");
            if (panel && !panel.hidden) {
                closeOpenMenu();
                if (!panel.hidden) hideSettingsPanel(true);
                byID("video-picture-button")?.focus({ preventScroll: true });
                return;
            }
            void closeVideoModal();
        },
        initialFocus: () => playBtnEl || closeBtnEl,
        restoreFocus: "#file-list",
    });
    bindEncryptedMediaLifecycle();
    nativeStateRouter.bind();
    renderSpeedOptions();
    transport.bind();
    unbindSpeedMenu = bindSpeedMenu();
    bindSettingsPanel();
    bindVideoPlaylist();
    unbindVideoDOM = bindVideoDOM(videoDOM, {
        close: () => { void closeVideoModal(); },
        retry: retryVideoOpen,
        toggleFullscreen: () => { void geometry?.toggleFullscreen(); },
        pointerMove: handleVideoPointerMove,
        stageClick: handleStageClick,
        stageDoubleClick: handleStageDoubleClick,
        keydown: handleVideoShortcut,
        resize: () => geometry?.handleWindowResize(() => {
            syncActivePanelGeometry();
            applyHtmlPicture();
        }),
    });
    geometry.observeControlsSize(() => {
        syncActivePanelGeometry();
        applyHtmlPicture();
    });
    applyState(EMPTY_PLAYER_STATE);
    return teardownVideoModal;
}
