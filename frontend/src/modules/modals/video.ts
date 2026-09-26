import { htmlPictureStyle, loadPlaybackPreferences, normalizePlaybackPreferences, savePlaybackPreferences, type PlaybackPreferences, type PictureMode } from "../video/playback-preferences";
import {
    attachNativeMedia,
    closeMedia,
    closeNativeMedia,
    getMediaStats,
    isAndroidPlatform,
    isIOSPlatform,
    isMobilePlatform,
    onRuntimeEvent,
    openMedia,
    openNativeMedia,
    setImmersive,
    updateMediaPlayback,
    type MediaOpenResult,
    type NativeMediaOpenResult,
    type NativeMediaRect,
} from "../../api";

import { formatBytes } from "../../utils";
import { isIOSPlayableVideo, isRemuxableVideo, isWebviewDirectVideo, videoFormatLabel } from "../media-types";
import { appActions } from "../app-actions";
import { accessEncryptedResource } from "../encryption";
import { prefersNativePlayer, rememberNativePlayer } from "../video/native-memory";
import {
    SerialPlaybackTransitions,
    capturePlaybackIntent,
    shouldFallbackFromHtmlMediaError,
    type PlaybackIntent,
} from "../video/playback-lifecycle";
import { type NativeMediaTrack } from "../video/media-tracks";
import {
    EMPTY_PLAYER_STATE,
    HtmlVideoAdapter,
    NativeMediaStateRouter,
    NativeMpvAdapter,
    type PlayerAdapter,
    type PlayerState,
    type TrackSwitching,
} from "../video/player-adapters";
import { errorDetail, errorMessage } from "../video/playback-errors";
import { handleMenuKeydown } from "../video/menu-keyboard";
import {
    nextPresetRate,
    parseCustomPlaybackRate,
    speedMenuMarkup,
    syncSpeedControls,
} from "../video/speed-menu-view";
import { StreamActivityMonitor } from "../video/stream-activity";
import { TrackPicker, type TrackPickerHost } from "../video/track-picker";
import {
    createActivePlaylist,
    normalizeVideoTarget,
    playlistItemIdentity,
    playlistViewItems,
    type ActiveVideoPlaylist,
    type VideoOpenTarget,
    type VideoPlaylistLaunch,
} from "../video/video-queue";
import { attachHls, prefersJsPlayer, type HlsSource } from "../video/hls-source";
import { MediaPrefetcher, readyToPrefetch, warmMediaEdges } from "../video/video-prefetch";
import { VideoGeometryController } from "../video/video-geometry";
import { SEEK_STEP_SECONDS, VOLUME_STEP, VideoTransportController } from "../video/video-transport";
import { bindVideoDOM, byID, collectVideoDOM, type VideoDOM } from "../video/video-dom";
import { activateModalOwnership, deactivateModalOwnership, installModalA11y } from "../../ui/modals/modal-a11y";
import { bindTouchGestures, type TouchGestureHandlers } from "../../ui/preview/touch-gestures";
import { videoPlaybackPreferences } from "../../ui/video/video-preferences-store";
import { loadAutoNextPreference } from "../video/video-playlist";
import {
    resetVideoPlaylist,
    setVideoPlaylist,
    setVideoPlaylistAutoNext,
    setVideoPlaylistCurrentIndex,
    setVideoPlaylistOpen,
    setVideoPlaylistSwitching,
} from "../../ui/video/video-playlist-store";

export type { VideoOpenTarget, VideoPlaylistLaunch } from "../video/video-queue";

const CHROME_HIDE_DELAY_MS = 2500;
const LOADING_DEBOUNCE_MS = 250;
const PLAYBACK_HINT_INTERVAL_MS = 1000;

interface VideoOpenAttempt {
    generation: number;
    target: VideoOpenTarget;
    htmlFailureHandled: boolean;
    nativeFallbackRequested: boolean;
    pausedByUser: boolean;
    playbackIntent: PlaybackIntent | null;
}

let playbackPreferences = loadPlaybackPreferences();
const SETTINGS_SECTIONS = ["picture", "audio", "subtitle", "speed"] as const;
type SettingsSection = (typeof SETTINGS_SECTIONS)[number];
let settingsSection: SettingsSection | null = null;
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
let mediaMetaBaseText = "";
let mediaMetaBytes = 0;
let a11y: ReturnType<typeof installModalA11y> | null = null;
let videoHostObserver: MutationObserver | null = null;
let videoHostEl: HTMLElement | null = null;
let unbindVideoDOM: (() => void) | null = null;
let unbindSpeedMenu: (() => void) | null = null;
let unbindTouchGestures: (() => void) | null = null;
// Where the last stage pointer came from: touch taps go through the phone
// recogniser, and a tap that began on the chrome is the chrome's to handle.
let stagePointerTouch = false;
let stagePointerOnChrome = false;
let videoDOM: VideoDOM | null = null;
let geometry: VideoGeometryController | null = null;
let transport: VideoTransportController | null = null;
let videoSetupComplete = false;

function isOpen() {
    return Boolean(modalEl && modalEl.style.display !== "none");
}

// The throughput line in the title bar and the buffering status both read from
// here. It is given accessors rather than values because it outlives any one
// media session: the token, the error state and the file's size all change
// under it while it keeps polling.
const streamActivity = new StreamActivityMonitor({
    readStats: (token) => getMediaStats(token),
    activeToken: () => activeMediaToken,
    hasError: () => hasError,
    mediaBytes: () => mediaMetaBytes,
    durationSeconds: () => currentState.duration,
    changed: () => {
        renderMediaMeta();
        updateLoadingStatus();
    },
});

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
    const activity = streamActivity.label;
    if (activity === "Rate-limited") {
        loadingStatusEl.textContent = "Buffering · Rate-limited";
        return;
    }
    if (activity.startsWith("Streaming ")) {
        loadingStatusEl.textContent = `Buffering · ${activity.slice("Streaming ".length)}`;
        return;
    }
    loadingStatusEl.textContent = "Buffering";
}

function setLoadingStatusOverride(message: string) {
    loadingStatusOverride = message;
    updateLoadingStatus();
}


/**
 * What the error's primary button does. Null means Retry, which is right when
 * the failure might not recur. A failure that will repeat identically forever
 * gets an action that can actually help instead.
 */
// ErrorAction is the one thing an error offers the reader beyond Retry, for the
// failures where retrying is not the answer.
type ErrorAction = { label: string; run: () => void };

let errorPrimaryAction: ErrorAction | null = null;

function setError(message: string, primary: ErrorAction | null = null) {
    hasError = true;
    errorPrimaryAction = primary;
    if (errorRetryBtnEl) errorRetryBtnEl.textContent = primary ? primary.label : "Retry";
    loadingStatusOverride = "";
    setLoading(false);
    streamActivity.stop();
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
    errorPrimaryAction = null;
    if (errorRetryBtnEl) errorRetryBtnEl.textContent = "Retry";
    errorMessageEl?.replaceChildren();
    if (errorEl) errorEl.style.display = "none";
    modalEl?.classList.remove("is-video-error");
    if (restoreFocus && isOpen()) (closeBtnEl || playBtnEl)?.focus({ preventScroll: true });
}

function retryVideoOpen() {
    if (!hasError || !isOpen()) return;
    if (errorPrimaryAction) {
        const run = errorPrimaryAction.run;
        run();
        return;
    }
    const target = activeOpenAttempt?.target;
    if (!target) return;
    void openVideoTarget(target, null);
}

function handleHtmlPlaybackError(detail: string) {
    console.error("HTML video playback failed:", detail);
    setError("The embedded player could not continue playing this video. Try again.");
}


// Keep available audio and subtitle tracks discoverable, even with one track. A pill appearing changes the
// controls height, so the native viewport is re-measured.
// trackSwitchingPlayer is the active player when it is one the pills can drive.
// A standalone native window owns its own controls, so the pills do not apply
// to it even though mpv is behind it.
function trackSwitchingPlayer(): TrackSwitching | null {
    if (activeNative && activeNative.presentation === "standalone") return null;
    if (activeAdapter instanceof NativeMpvAdapter) return activeAdapter;
    if (activeAdapter instanceof HtmlVideoAdapter) return activeAdapter;
    return null;
}

function syncNativeTracks(tracks: NativeMediaTrack[]) {
    const available = trackSwitchingPlayer() !== null;
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

// The pills share the player's chrome, its settings panel and its notion of
// which player can switch tracks; the picker itself owns none of that, so it is
// handed this view of the controller rather than reading these directly.
const trackPickerHost: TrackPickerHost = {
    openSettingsSection: () => settingsSection,
    hideSettingsPanel: () => hideSettingsPanel(),
    holdChrome: clearChromeTimer,
    // A menu closing only restarts the fade when the player is in a state that
    // fades at all: paused, errored or shut, the chrome stays put.
    releaseChrome: () => {
        if (isOpen() && !currentState.paused && !hasError) scheduleChromeHide();
    },
    revealChrome,
    trackSwitchingPlayer,
    returnFocus: () => byID("video-picture-button")?.focus({ preventScroll: true }),
};

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
// A phone's control row is a few hundred pixels wide and every pill on it
// competes for the same space. "Original" alone is wide enough to push the row
// onto a second line, so the pill says the short form and the description the
// button announces stays the full one.
const PICTURE_PILL_LABELS: Record<PictureMode, string> = { ...PICTURE_LABELS, original: "Orig" };

function syncAspectButton() {
    const button = byID("video-aspect-button");
    if (!button) return;
    const mode = playbackPreferences.pictureMode;
    const label = PICTURE_LABELS[mode];
    button.textContent = isMobilePlatform() ? PICTURE_PILL_LABELS[mode] : label;
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

function showSettingsPanel(section: SettingsSection) {
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
    if (picture) picture.hidden = section !== "picture";
    if (appearance) appearance.hidden = section !== "subtitle";
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
        const requested = button.dataset.settingsSection;
        showSettingsSection(SETTINGS_SECTIONS.find((value) => value === requested) ?? "picture");
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

// showSettingsSection swaps which section the open panel is showing. Routing
// through the pickers instead would close the panel first and reopen it, which
// reads as the popover dismissing itself on a tab click.
function showSettingsSection(section: SettingsSection) {
    speedMenuEl?.classList.remove("is-open");
    for (const picker of trackPickers()) picker.setMenuOpen(false);
    showSettingsPanel(section);
    if (section === "audio") audioPicker?.setMenuOpen(true);
    else if (section === "subtitle") subtitlePicker?.setMenuOpen(true);
    else if (section === "speed") {
        speedMenuEl?.classList.add("is-open");
        clearChromeTimer();
        requestAnimationFrame(() => {
            if (isSpeedMenuOpen() && !speedMenuEl?.contains(document.activeElement)) {
                byID("video-speed-slider")?.focus({ preventScroll: true });
            }
        });
    }
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

// The next playlist item is opened and warmed once the current one is close to
// the end and fully buffered, so auto-next starts without a cold round-trip to
// Telegram. A single slot is enough: only the immediate next item is useful.
const mediaPrefetcher = new MediaPrefetcher<MediaOpenResult>({
    open: (id) => openMedia(id),
    close: (token) => safelyCloseMedia(token),
    warm: (session) => warmMediaEdges(session.url, session.info.plaintextSize || session.info.storedSize || 0),
});

function nextPlaylistTarget(): VideoOpenTarget | null {
    if (!activePlaylist?.autoNext) return null;
    const next = activePlaylist.items[activePlaylist.currentIndex + 1];
    return next ? normalizeVideoTarget(next) : null;
}

function maybePrefetchNext(state: PlayerState) {
    if (!isOpen() || hasError || !readyToPrefetch(state)) return;
    const next = nextPlaylistTarget();
    if (!next || mediaPrefetcher.holds(next.id)) return;
    void mediaPrefetcher.prepare(next.id);
}

function applyState(state: PlayerState) {
    const wasPaused = currentState.paused;
    currentState = state;
    if (!state.loading && loadingStatusOverride) {
        loadingStatusOverride = "";
    }
    schedulePlaybackHint(state);
    transport?.sync(state);
    syncSpeedControls(speedBtnEl, speedMenuEl, state.rate);
    syncNativeTracks(state.tracks);
    applyHtmlPicture();
    setLoading(state.loading);
    streamActivity.sync();
    maybePrefetchNext(state);
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
    streamActivity.stop();
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

// The phone shows the first-frame thumbnail, blurred, until the stream paints
// (design-5). It is fetched off the element first: a poster the element cannot
// load fires `error` on the video, and the thumbnail endpoint has no mpv
// behind it on the phones.
function preloadPoster(url: string, token: string) {
    const image = new Image();
    image.onload = () => {
        if (videoEl && isOpen() && activeMediaToken === token) videoEl.poster = url;
    };
    image.src = url;
}

function updateMediaText(name: string, size: number) {
    if (filenameEl) filenameEl.textContent = name || "Video";
    mediaMetaBaseText = `${videoFormatLabel(name)}${size ? ` · ${formatBytes(size)}` : ""}`;
    mediaMetaBytes = size || 0;
    renderMediaMeta();
}

function renderMediaMeta() {
    if (!metaEl) return;
    const activity = streamActivity.label;
    metaEl.textContent = activity ? `${mediaMetaBaseText} · ${activity}` : mediaMetaBaseText;
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
    void mediaPrefetcher.discard();
    activePlaylist = createActivePlaylist(target, loadAutoNextPreference(), launch);
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
    if (index !== playlist.currentIndex + 1) await mediaPrefetcher.discard();
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
    // Nothing is queued up any more, so stop holding a reader open for it.
    if (!enabled) void mediaPrefetcher.discard();
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
        // A prefetched session was opened while the file was already unlocked,
        // so only a cold open can reach the vault prompt. A null here is the
        // user dismissing that prompt, which is not an error to surface.
        opened = mediaPrefetcher.take(attempt.target.id) ?? await accessEncryptedResource(
            Boolean(attempt.target.encrypted),
            () => openMedia(attempt.target.id),
        );
        if (!opened) {
            if (isCurrent()) await closeVideoModal();
            return;
        }
        if (!isCurrent() || !isOpen()) {
            await safelyCloseMedia(opened.token);
            return;
        }
        if (!opened.url || !opened.token) {
            await safelyCloseMedia(opened.token);
            opened = null;
            throw new Error("media session did not return a playable URL");
        }

        // Android's Chromium can neither list a file's soundtracks nor switch
        // between them, so a file that carries more than one is handed to a
        // JavaScript player pointed at the same remuxed playlist iOS uses. A
        // file with one soundtrack keeps the native path, which is faster and
        // has nothing to gain from the detour.
        let hlsSource: HlsSource | null = null;
        if (opened.hlsUrl && isAndroidPlatform() && await prefersJsPlayer(opened.hlsUrl)) {
            if (!isCurrent() || !isOpen()) {
                await safelyCloseMedia(opened.token);
                return;
            }
            hlsSource = await attachHls(videoEl!, opened.hlsUrl, () => adapter?.refresh());
        }

        const displayName = opened.info.name || opened.name || attempt.target.name || "Video";
        const displaySize = opened.info.plaintextSize || opened.info.storedSize || attempt.target.size || 0;
        updateMediaText(displayName, displaySize);
        transport?.beginSession(opened.thumbnailUrl);
        activeMediaToken = opened.token;
        if (isMobilePlatform() && opened.thumbnailUrl) preloadPoster(`${opened.thumbnailUrl}?t=0`, opened.token);
        activeMediaEncrypted = Boolean(opened.info.encrypted);

        adapter = new HtmlVideoAdapter(videoEl!, opened, {
            mediaError: (code, state) => handleHtmlMediaError(attempt, adapter!, code, state),
            playbackError: handleHtmlPlaybackError,
            revealChrome,
            mediaEnded: () => handleNaturalMediaEnd(attempt, adapter!),
        }, hlsSource);
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

    // Phones have no native player to promote to; the HTML error is final there.
    const undecodable = shouldFallbackFromHtmlMediaError(code);
    if (undecodable && !isMobilePlatform()) {
        rememberNativePlayer(attempt.target);
        attempt.nativeFallbackRequested = true;
        const intent = capturePlaybackIntent(state, attempt.pausedByUser);
        setLoadingStatusOverride("Switching to a compatible player...");
        setLoading(true);
        void promoteHtmlToNative(attempt, adapter, intent);
        return;
    }

    const message = code === 2
        ? "The video stream was interrupted. Check your connection and try again."
        : undecodable
            ? "This video can't be played on this device."
            : "The embedded player could not continue playing this video. Try again.";
    // A repackaged container that still will not decode has nothing left to
    // retry: the streams inside are ones this device has no decoder for. Point
    // at the one thing that can still work rather than at a button that cannot.
    const action = undecodable && isRemuxableVideo(attempt.target.name)
        ? downloadInstead(attempt.target)
        : null;
    void playbackTransitions.run(attempt.generation, async (isCurrent) => {
        await releaseActive();
        if (isCurrent()) setError(message, action);
    });
}

// downloadInstead is the escape from a file this device cannot play: the share
// sheet hands it to an app that can.
function downloadInstead(target: VideoOpenTarget): ErrorAction {
    return {
        label: "Download",
        run: () => {
            appActions().downloadFile({ id: target.id, name: target.name, size: target.size || 0 });
            void closeVideoModal();
        },
    };
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
        // Re-attaching an existing session never re-prompts: its token was only
        // handed out after the file was unlocked once.
        const result = existing
            ? await attachNativeMedia(existing.token, rect)
            : await accessEncryptedResource(
                Boolean(attempt.target.encrypted),
                () => openNativeMedia(attempt.target.id, rect),
            );
        if (!result) {
            if (isCurrent()) await closeVideoModal();
            return;
        }
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
    videoEl.removeAttribute("poster");
    clearError();
    setLoadingStatusOverride("");
    setLoading(true);
    setChromeVisible(true);
    modalEl.style.display = "flex";
    modalEl.setAttribute("aria-hidden", "false");
    // Take the phone's system bars for the duration. A player is the one
    // surface that wants the whole screen, and on Android it is also the only
    // way to be rid of the band the system paints behind the navigation
    // buttons, which lands on top of the picture.
    setImmersive(true);
    activateModalOwnership(modalEl);
    a11y?.activate();
    void geometry?.syncFullscreenState();

    // A container iOS cannot demux and the backend will not repackage fails no
    // matter how long it is given, so do not open a media session for it: that
    // would spend Telegram bandwidth and API calls to reach a guaranteed
    // failure, and leave the reader looking at a Retry button that can never
    // work. Offer the download instead, which hands the file to the share sheet
    // and on to a player that can open it.
    if (isIOSPlatform() && !isIOSPlayableVideo(target.name) && !isRemuxableVideo(target.name)) {
        const format = videoFormatLabel(target.name);
        setError(
            `iOS cannot open ${format} files. Download it to play in another app.`,
            downloadInstead(target),
        );
        return;
    }

    await playbackTransitions.run(attempt.generation, async (isCurrent) => {
        await releaseActive();
        if (!isCurrent() || !isOpen()) return;
        setLoadingStatusOverride("");
        setLoading(true);
        // A container the webview handles goes to the HTML player, unless it
        // already failed to decode this very file: then the native player
        // opens first and the failed attempt is not paid again. Phones have
        // no native player, so every container goes through <video>.
        if (isMobilePlatform() || (isWebviewDirectVideo(attempt.target.name) && !prefersNativePlayer(attempt.target))) {
            await openHtmlPlayback(attempt, isCurrent);
            return;
        }
        const rect = await geometry?.prepareNativeRect(isCurrent);
        if (!rect || !isCurrent()) return;
        // A warmed session is attached to rather than opened again, which skips
        // the Telegram round-trip a fresh native open would repeat.
        const warmed = mediaPrefetcher.take(attempt.target.id);
        await openNativePlayback(attempt, rect, isCurrent, warmed, attempt.playbackIntent);
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
    await mediaPrefetcher.discard();
    activeOpenAttempt = null;
    hideVideoPlaylist();
    activePlaylist = null;
    resetVideoPlaylist();
    clearChromeTimer();
    await geometry?.exitVideoFullscreen();
    if (!playbackTransitions.isCurrent(generation)) return;
    modalEl.style.display = "none";
    modalEl.setAttribute("aria-hidden", "true");
    setImmersive(false);
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
        if (!activeAdapter) return;
        activeAdapter.setSpeed(nextPresetRate(currentState.rate));
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

function handleVideoPointerMove(event: PointerEvent) {
    // A finger dragging across the phone player is a gesture, not a request
    // for the controls.
    if (event.pointerType === "touch" && isMobilePlatform()) return;
    revealChrome();
}

function targetIsVideoChrome(target: EventTarget | null) {
    const el = target instanceof Element ? target : null;
    return Boolean(el?.closest(".video-topbar, .video-controls, .video-center-controls, .video-error, .video-loading, .video-settings-panel, .video-playlist-panel"));
}

function handleStageClick(event: MouseEvent) {
    if (activeNative?.presentation === "standalone") return;
    if (stagePointerTouch && isMobilePlatform()) return;
    if (targetIsVideoChrome(event.target)) return;
    transport?.togglePlayback();
}

function handleStageDoubleClick(event: MouseEvent) {
    if (activeNative?.presentation === "standalone") return;
    if (stagePointerTouch && isMobilePlatform()) return;
    if (targetIsVideoChrome(event.target)) return;
    event.preventDefault();
    void geometry?.toggleFullscreen();
}

function trackStagePointer(event: PointerEvent) {
    stagePointerTouch = event.pointerType === "touch";
    stagePointerOnChrome = targetIsVideoChrome(event.target);
}

// Phone gestures: a tap toggles the controls, a double tap on either side
// seeks ten seconds (in the middle it toggles playback) and a swipe down
// closes the player.
function videoTouchHandlers(): TouchGestureHandlers {
    const shell = () => byID<HTMLElement>("video-shell");
    return {
        tap: () => {
            if (!isOpen() || hasError || stagePointerOnChrome) return;
            if (modalEl?.classList.contains("is-video-chrome-visible")) {
                clearChromeTimer();
                setChromeVisible(false);
            } else {
                revealChrome();
            }
        },
        doubleTap: (x) => {
            if (!isOpen() || hasError || stagePointerOnChrome || !stageEl) return;
            const { left, width } = stageEl.getBoundingClientRect();
            const across = (x - left) / Math.max(1, width);
            if (across < 1 / 3) transport?.seekBy(-SEEK_STEP_SECONDS);
            else if (across > 2 / 3) transport?.seekBy(SEEK_STEP_SECONDS);
            else transport?.togglePlayback();
        },
        dragStart: (axis) => axis === "y" && isOpen() && !stagePointerOnChrome && !isAnyMenuOpen(),
        drag: (_dx, dy) => {
            const el = shell();
            if (!el) return;
            const drop = Math.max(0, dy);
            el.style.transition = "none";
            el.style.transform = `translate3d(0, ${drop}px, 0)`;
            el.style.opacity = String(Math.max(0.4, 1 - drop / 480));
        },
        dragEnd: (_dx, dy, _axis, velocity) => {
            const el = shell();
            if (!el) return;
            el.style.transition = "";
            el.style.transform = "";
            el.style.opacity = "";
            if (dy > 120 || (dy > 32 && velocity > 0.6)) void closeVideoModal();
        },
    };
}

function bindEncryptedMediaLifecycle() {
    if (unsubscribeEncryptedMediaSessionsClosed) return;
    unsubscribeEncryptedMediaSessionsClosed = onRuntimeEvent("encrypted_media_sessions_closed", () => {
        // The warmed next item goes first, whatever is on screen. It was
        // opened while the vault was open and is taken without asking for a
        // password -- that is the point of warming one -- so a lock has to
        // drop it, or the next item plays from a session the backend has
        // already closed and the reader is never asked to unlock.
        void mediaPrefetcher.discard();
        if (!activeOpenAttempt || (!activeOpenAttempt.target.encrypted && !activeMediaEncrypted)) return;
        void closeVideoModal();
    });
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

    setImmersive(false);
    if (modalEl) deactivateModalOwnership(modalEl);
    a11y?.deactivate();
    a11y = null;
    unbindVideoDOM?.();
    unbindVideoDOM = null;
    unbindSpeedMenu?.();
    unbindSpeedMenu = null;
    unbindTouchGestures?.();
    unbindTouchGestures = null;
    stageEl?.removeEventListener("pointerdown", trackStagePointer);
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
    audioPicker = new TrackPicker("Audio", null, (player, id) => {
        if (id !== null) player.setAudioTrack(id);
    }, videoDOM.audioPicker, trackPickerHost);
    subtitlePicker = new TrackPicker(
        "Subtitles",
        "Off",
        (player, id) => player.setSubtitleTrack(id),
        videoDOM.subtitlePicker,
        trackPickerHost,
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
    if (speedMenuEl) speedMenuEl.innerHTML = speedMenuMarkup();
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
    if (isMobilePlatform()) {
        stageEl.addEventListener("pointerdown", trackStagePointer);
        unbindTouchGestures = bindTouchGestures(stageEl, videoTouchHandlers());
    }
    applyState(EMPTY_PLAYER_STATE);
    return teardownVideoModal;
}
