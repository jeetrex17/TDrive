/**
 * The video modal's coordinator.
 *
 * It owns exactly one thing: which player is currently attached to which media
 * session, and how ownership moves between them — opening, promoting the HTML
 * player to the native one, falling back, switching playlist items and closing.
 * Every surface around that (chrome, status, errors, the settings dock, the
 * playlist panel, input, transport, geometry) lives in `../video/*` and is wired
 * in here through explicit context objects.
 *
 * The session machinery stays in one place because it is a state machine over a
 * remote resource: a half-finished open must be able to see that a newer one has
 * superseded it, and every path out has to close the token it opened. Splitting
 * that across modules would split the invariant with it.
 */

import { loadPlaybackPreferences, normalizePlaybackPreferences, savePlaybackPreferences, htmlPictureStyle, type PlaybackPreferences } from "../video/playback-preferences";
import {
    attachNativeMedia,
    closeMedia,
    closeNativeMedia,
    isAndroidPlatform,
    isIOSPlatform,
    isMobilePlatform,
    onRuntimeEvent,
    openMedia,
    openNativeMedia,
    setImmersive,
    type MediaOpenResult,
    type NativeMediaOpenResult,
    type NativeMediaRect,
} from "../../api";

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
import {
    EMPTY_PLAYER_STATE,
    HtmlVideoAdapter,
    NativeMediaStateRouter,
    NativeMpvAdapter,
    type PlayerAdapter,
    type PlayerState,
    type TrackSwitching,
} from "../video/player-adapters";
import { attachHls, prefersJsPlayer, type HlsSource } from "../video/hls-source";
import { MediaPrefetcher, readyToPrefetch, warmMediaEdges } from "../video/video-prefetch";
import { VideoGeometryController } from "../video/video-geometry";
import { VideoTransportController } from "../video/video-transport";
import { bindVideoDOM, byID, collectVideoDOM, type VideoDOM } from "../video/video-dom";
import { VideoChromeController } from "../video/video-chrome";
import { VideoStatusController } from "../video/video-status";
import { PlaybackHintReporter } from "../video/playback-hints";
import { VideoErrorSurface, errorDetail, errorMessage, type ErrorAction } from "../video/video-error-surface";
import { VideoSettingsDock } from "../video/video-settings-dock";
import { VideoInputController } from "../video/video-input";
import {
    VideoPlaylistSession,
    normalizeVideoTarget,
    type VideoOpenTarget,
    type VideoPlaylistLaunch,
} from "../video/video-playlist-session";
import { activateModalOwnership, deactivateModalOwnership, installModalA11y } from "../../ui/modals/modal-a11y";
import { bindTouchGestures } from "../../ui/preview/touch-gestures";
import { videoPlaybackPreferences } from "../../ui/video/video-preferences-store";

export type { VideoPlaylistLaunch };

/** One open request, and the fallbacks it is still allowed to take. */
interface VideoOpenAttempt {
    generation: number;
    target: VideoOpenTarget;
    htmlFailureHandled: boolean;
    nativeFallbackRequested: boolean;
    pausedByUser: boolean;
    playbackIntent: PlaybackIntent | null;
}

let playbackPreferences = loadPlaybackPreferences();

let modalEl: HTMLElement | null = null;
let stageEl: HTMLElement | null = null;
let topbarEl: HTMLElement | null = null;
let controlsEl: HTMLElement | null = null;
let videoEl: HTMLVideoElement | null = null;

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
let a11y: ReturnType<typeof installModalA11y> | null = null;
let videoHostObserver: MutationObserver | null = null;
let videoHostEl: HTMLElement | null = null;
let unbindVideoDOM: (() => void) | null = null;
let unbindTouchGestures: (() => void) | null = null;
let videoDOM: VideoDOM | null = null;
let geometry: VideoGeometryController | null = null;
let transport: VideoTransportController | null = null;
let status: VideoStatusController | null = null;
let errors: VideoErrorSurface | null = null;
let dock: VideoSettingsDock | null = null;
let input: VideoInputController | null = null;
let videoSetupComplete = false;

function isOpen(): boolean {
    return Boolean(modalEl && modalEl.style.display !== "none");
}

function hasError(): boolean {
    return Boolean(errors?.active);
}

function isNativeFallbackActive(): boolean {
    return Boolean(activeNative && !activeNative.htmlControls && activeNative.presentation !== "standalone");
}

// The chrome and the playlist outlive any one activation of the modal, so both
// are wired to the module's live element rather than to a collected snapshot.
const chrome: VideoChromeController = new VideoChromeController({
    modal: () => modalEl,
    isOpen,
    isPaused: () => currentState.paused,
    hasError,
    isHeldOpen: () => isAnyMenuOpen() || Boolean(transport?.isScrubberTooltipActive()),
    isNativeFallbackActive,
});

const playlist: VideoPlaylistSession = new VideoPlaylistSession({
    modal: () => modalEl,
    chrome,
    closeMenus: () => closeMenus(),
    syncPanelGeometry: syncActivePanelGeometry,
    syncViewportInsets: () => geometry?.syncFallbackNativeViewportInsets(),
    scheduleNativeResize: () => geometry?.scheduleNativeResize(),
    applyPicture: applyHtmlPicture,
    isOpen,
});

const hints = new PlaybackHintReporter({
    token: () => activeMediaToken,
    state: () => currentState,
});

/**
 * The next playlist item is opened and warmed once the current one is close to
 * the end and fully buffered, so auto-next starts without a cold round-trip to
 * Telegram. A single slot is enough: only the immediate next item is useful.
 */
const mediaPrefetcher = new MediaPrefetcher<MediaOpenResult>({
    open: (id) => openMedia(id),
    close: (token) => safelyCloseMedia(token),
    warm: (session) => warmMediaEdges(session.url, session.info.plaintextSize || session.info.storedSize || 0),
});

// --- popovers -------------------------------------------------------------

function isAnyMenuOpen(): boolean {
    return playlist.isPanelOpen || Boolean(dock?.hasOpenPopover());
}

/** Closes every popover except the one about to open. */
function closeMenus(except: Parameters<VideoSettingsDock["closeAll"]>[0] = null) {
    dock?.closeAll(except);
}

/** Closes whichever popover or panel is open, returning whether one was. */
function closeOpenMenu() {
    if (playlist.isPanelOpen) {
        hideVideoPlaylist(true);
        return true;
    }
    if (!isAnyMenuOpen()) return false;
    dock?.closeOpen();
    return true;
}

/** The panel currently occupying the slot between the topbar and the controls. */
function getActiveVideoPanel(): HTMLElement | null {
    if (dock?.openSection != null) return dock.panel;
    if (playlist.isPanelOpen) return playlist.panel;
    return null;
}

/**
 * A panel does not overlap the chrome, so it is given the gap between them as
 * CSS custom properties rather than a fixed height.
 */
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

// --- picture --------------------------------------------------------------

function applyHtmlPicture() {
    if (!videoEl || !stageEl) return;
    const rect = stageEl.getBoundingClientRect();
    const style = htmlPictureStyle(playbackPreferences.pictureMode, rect.width, rect.height, videoEl.videoWidth, videoEl.videoHeight);
    Object.assign(videoEl.style, style);
}

export function updatePlaybackPreferences(value: PlaybackPreferences): void {
    playbackPreferences = normalizePlaybackPreferences(value);
    videoPlaybackPreferences.set(playbackPreferences);
    savePlaybackPreferences(playbackPreferences);
    dock?.syncAspectButton();
    if (activeAdapter instanceof NativeMpvAdapter) activeAdapter.applyPreferences(playbackPreferences);
    applyHtmlPicture();
}

// --- player state ---------------------------------------------------------

/**
 * The active player when it is one the track pills can drive. A standalone
 * native window owns its own controls, so the pills do not apply to it even
 * though mpv is behind it.
 */
function trackSwitchingPlayer(): TrackSwitching | null {
    if (activeNative && activeNative.presentation === "standalone") return null;
    if (activeAdapter instanceof NativeMpvAdapter) return activeAdapter;
    if (activeAdapter instanceof HtmlVideoAdapter) return activeAdapter;
    return null;
}

function applyState(state: PlayerState) {
    const wasPaused = currentState.paused;
    currentState = state;
    if (!state.loading) status?.clearOverride();
    hints.schedule(state);
    transport?.sync(state);
    dock?.speed.sync(state);
    dock?.syncTracks(state.tracks);
    applyHtmlPicture();
    status?.setLoading(state.loading);
    status?.syncPolling(activeMediaToken);
    maybePrefetchNext(state);
    if (state.paused || hasError()) {
        chrome.pin();
    } else if (wasPaused) {
        chrome.scheduleHide();
    }
}

function maybePrefetchNext(state: PlayerState) {
    if (!isOpen() || hasError() || !readyToPrefetch(state)) return;
    const next = playlist.nextTarget();
    if (!next || mediaPrefetcher.holds(next.id)) return;
    void mediaPrefetcher.prepare(next.id);
}

// --- session ownership ----------------------------------------------------

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

/**
 * Drops every reference to the current session and returns the adapter, so the
 * caller decides whether it is closed or handed to the native player.
 */
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
    hints.clear();
    status?.syncPolling("");
    transport?.resetSession();
    closeMenus();
    dock?.hidePanel();
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

// --- errors ---------------------------------------------------------------

function setError(message: string, primary: ErrorAction | null = null) {
    errors?.show(message, primary);
}

/**
 * downloadInstead is the escape from a file this device cannot play: the share
 * sheet hands it to an app that can.
 */
function downloadInstead(target: VideoOpenTarget): ErrorAction {
    return {
        label: "Download",
        run: () => {
            appActions().downloadFile({ id: target.id, name: target.name, size: target.size || 0 });
            void closeVideoModal();
        },
    };
}

function handleHtmlPlaybackError(detail: string) {
    console.error("HTML video playback failed:", detail);
    setError("The embedded player could not continue playing this video. Try again.");
}

// --- playlist -------------------------------------------------------------

export function hideVideoPlaylist(restoreFocus = false): void {
    playlist.hidePanel(restoreFocus);
}

export function selectVideoPlaylistItem(index: number): void {
    void switchVideoPlaylistItem(index, true);
}

export function updateVideoAutoNext(enabled: boolean): void {
    playlist.setAutoNext(Boolean(enabled));
    // Nothing is queued up any more, so stop holding a reader open for it.
    if (!enabled) void mediaPrefetcher.discard();
}

/** Auto-next resumes from the top of the next file at the same volume and rate. */
function nextPlaylistPlaybackIntent(): PlaybackIntent {
    const intent = capturePlaybackIntent(currentState, false);
    intent.currentTime = 0;
    intent.paused = false;
    return intent;
}

async function switchVideoPlaylistItem(index: number, closePanel: boolean): Promise<void> {
    const target = playlist.itemAt(index);
    if (!target) return;
    const queue = playlist.playlist;
    const from = playlist.currentIndex();
    if (index === from) {
        if (closePanel) hideVideoPlaylist(true);
        return;
    }
    // Only the immediate next item was ever warmed; a jump elsewhere invalidates it.
    if (index !== from + 1) await mediaPrefetcher.discard();
    const intent = nextPlaylistPlaybackIntent();
    playlist.setCurrentIndex(index);
    playlist.markSwitching(playlist.identity(target, index));
    if (closePanel) hideVideoPlaylist(true);
    try {
        await openVideoTarget(target, intent);
    } finally {
        // A newer switch — or a whole new queue — owns the spinner now; clearing
        // it here would hide theirs.
        if (playlist.playlist === queue && playlist.currentIndex() === index) playlist.markSwitching(null);
    }
}

function handleNaturalMediaEnd(attempt: VideoOpenAttempt, adapter: PlayerAdapter) {
    if (
        activeOpenAttempt !== attempt ||
        activeAdapter !== adapter ||
        !playbackTransitions.isCurrent(attempt.generation) ||
        !playlist.autoNext
    ) {
        return;
    }
    const nextIndex = playlist.currentIndex() + 1;
    if (!playlist.itemAt(nextIndex)) {
        chrome.pin();
        return;
    }
    void switchVideoPlaylistItem(nextIndex, false);
}

// --- opening --------------------------------------------------------------

/**
 * The phone shows the first-frame thumbnail, blurred, until the stream paints
 * (design-5). It is fetched off the element first: a poster the element cannot
 * load fires `error` on the video, and the thumbnail endpoint has no mpv behind
 * it on the phones.
 */
function preloadPoster(url: string, token: string) {
    const image = new Image();
    image.onload = () => {
        if (videoEl && isOpen() && activeMediaToken === token) videoEl.poster = url;
    };
    image.src = url;
}

async function openHtmlPlayback(attempt: VideoOpenAttempt, isCurrent: () => boolean) {
    let opened: MediaOpenResult | null = null;
    let adapter: HtmlVideoAdapter | null = null;
    try {
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
        status?.setMediaText(displayName, displaySize);
        transport?.beginSession(opened.thumbnailUrl);
        activeMediaToken = opened.token;
        if (isMobilePlatform() && opened.thumbnailUrl) preloadPoster(`${opened.thumbnailUrl}?t=0`, opened.token);
        activeMediaEncrypted = Boolean(opened.info.encrypted);

        adapter = new HtmlVideoAdapter(videoEl!, opened, {
            mediaError: (code, state) => handleHtmlMediaError(attempt, adapter!, code, state),
            playbackError: handleHtmlPlaybackError,
            revealChrome: () => chrome.reveal(),
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
        status?.setOverride("Switching to a compatible player...");
        status?.setLoading(true);
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

async function promoteHtmlToNative(
    attempt: VideoOpenAttempt,
    adapter: HtmlVideoAdapter,
    intent: PlaybackIntent,
) {
    await playbackTransitions.run(attempt.generation, async (isCurrent) => {
        if (!isCurrent() || activeAdapter !== adapter || !attempt.nativeFallbackRequested) return;
        const existing = detachHtmlForNative(adapter);
        if (!existing) return;

        // detachHtmlForNative reset the status line, so the explanation for the
        // pause the reader is looking at has to be restated.
        status?.setOverride("Switching to a compatible player...");
        status?.setLoading(true);
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
    // Who is responsible for closing the session if this throws.
    let owner: "none" | "html" | "native" | "adapter" = existing ? "html" : "none";
    try {
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
    status?.setMediaText(displayName, displaySize);
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

    // mpv cannot seek before it knows the duration, so a restored position waits
    // for the first state that carries one.
    let positionRestored = !intent || intent.currentTime <= 0;
    unsubscribeState = adapter.subscribe((state) => {
        if (!playbackTransitions.isCurrent(attempt.generation) || activeAdapter !== adapter) return;
        applyState(state);
        if (!positionRestored && state.duration > 0) {
            positionRestored = true;
            adapter.seekAbsolute(intent!.currentTime);
        }
    });
    chrome.setVisible(true);
    if (!standalone) geometry?.scheduleNativeResize();
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

async function openVideoTarget(target: VideoOpenTarget, playbackIntent: PlaybackIntent | null): Promise<void> {
    const host = byID<HTMLElement>("video-modal");
    if (videoHostEl !== host || !videoSetupComplete) activateVideoModal();
    if (!modalEl || !videoEl || !videoDOM?.filename || !videoDOM?.meta) return;

    const attempt: VideoOpenAttempt = {
        generation: playbackTransitions.begin(),
        target,
        htmlFailureHandled: false,
        nativeFallbackRequested: false,
        pausedByUser: false,
        playbackIntent,
    };
    activeOpenAttempt = attempt;

    status?.setMediaText(target.name || "Video", target.size || 0);
    videoEl.removeAttribute("poster");
    errors?.clear();
    status?.setOverride("");
    status?.setLoading(true);
    chrome.setVisible(true);
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
        status?.setOverride("");
        status?.setLoading(true);
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

export async function openVideoModal(target: VideoOpenTarget, launch?: VideoPlaylistLaunch): Promise<void> {
    const normalized = normalizeVideoTarget(target);
    if (!normalized) return;
    const host = byID<HTMLElement>("video-modal");
    if (videoHostEl !== host || !videoSetupComplete) activateVideoModal();
    if (!modalEl || !videoEl || !videoDOM?.filename || !videoDOM?.meta) return;
    playlist.hidePanel();
    void mediaPrefetcher.discard();
    playlist.install(normalized, launch);
    await openVideoTarget(normalized, null);
}

export async function closeVideoModal() {
    if (!modalEl) return;
    const generation = playbackTransitions.begin();
    await mediaPrefetcher.discard();
    activeOpenAttempt = null;
    playlist.hidePanel();
    playlist.reset();
    chrome.clearTimer();
    await geometry?.exitVideoFullscreen();
    if (!playbackTransitions.isCurrent(generation)) return;
    modalEl.style.display = "none";
    modalEl.setAttribute("aria-hidden", "true");
    setImmersive(false);
    deactivateModalOwnership(modalEl);
    a11y?.deactivate();
    await playbackTransitions.run(generation, async () => releaseActive());
    if (!playbackTransitions.isCurrent(generation)) return;
    errors?.clear();
    status?.setOverride("");
    status?.setLoading(false);
}

// --- lifecycle ------------------------------------------------------------

function bindEncryptedMediaLifecycle() {
    if (unsubscribeEncryptedMediaSessionsClosed) return;
    unsubscribeEncryptedMediaSessionsClosed = onRuntimeEvent("encrypted_media_sessions_closed", () => {
        if (!activeOpenAttempt || (!activeOpenAttempt.target.encrypted && !activeMediaEncrypted)) return;
        void closeVideoModal();
    });
}

export function teardownVideoModal(): void {
    playbackTransitions.begin();
    activeOpenAttempt = null;
    playlist.hidePanel();
    playlist.reset();
    chrome.clearTimer();
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
    dock?.destroy();
    unbindTouchGestures?.();
    unbindTouchGestures = null;
    if (input && stageEl) stageEl.removeEventListener("pointerdown", input.trackStagePointer);
    nativeStateRouter.unbind();
    unsubscribeEncryptedMediaSessionsClosed?.();
    unsubscribeEncryptedMediaSessionsClosed = null;
    void releaseActive();
    transport?.destroy();
    geometry?.destroy();
    status?.destroy();
    transport = null;
    geometry = null;
    status = null;
    errors = null;
    dock = null;
    input = null;
    videoDOM = null;

    videoHostObserver?.disconnect();
    videoHostObserver = null;

    modalEl = null;
    stageEl = null;
    topbarEl = null;
    controlsEl = null;
    videoEl = null;
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
    // The modal is Svelte-owned, so it can be unmounted out from under this
    // controller; the observer is what turns that into a clean teardown.
    videoHostObserver = new MutationObserver(() => {
        if (!host.isConnected) teardownVideoModal();
    });
    videoHostObserver.observe(document.body, { childList: true, subtree: true });

    const dom = collectVideoDOM();
    videoDOM = dom;
    modalEl = dom.modal;
    stageEl = dom.stage;
    topbarEl = dom.topbar;
    controlsEl = dom.controls;
    videoEl = dom.video;

    if (!modalEl || !videoEl || !stageEl) {
        console.error("Video modal setup failed. Missing #video-modal, #video-stage, or #video-player.");
        teardownVideoModal();
        return () => {};
    }

    status = new VideoStatusController({
        dom,
        hasAdapter: () => Boolean(activeAdapter),
        hasError,
        duration: () => currentState.duration,
    });
    errors = new VideoErrorSurface({
        dom,
        chrome,
        status,
        isOpen,
        retryCurrent: () => {
            const target = activeOpenAttempt?.target;
            if (target) void openVideoTarget(target, null);
        },
    });
    geometry = new VideoGeometryController({
        dom,
        getNative: () => activeNative,
        hasActivePlayer: () => Boolean(activeAdapter),
        hasError,
        isOpen,
        getActivePanel: getActiveVideoPanel,
        revealChrome: () => chrome.reveal(),
        reportSurfaceError: () => setError("Could not prepare the video player. Try again."),
    });
    transport = new VideoTransportController({
        dom,
        getAdapter: () => activeAdapter,
        getState: () => currentState,
        getNative: () => activeNative,
        hasError,
        isNativeFallbackActive,
        markPausedByUser: (paused) => {
            if (activeOpenAttempt) activeOpenAttempt.pausedByUser = paused;
        },
        revealChrome: () => chrome.reveal(),
        scheduleChromeHide: () => chrome.scheduleHide(),
        geometryChanged: () => {
            syncActivePanelGeometry();
            geometry?.scheduleNativeResize();
        },
        refreshFullscreenAvailability: () => geometry?.refreshFullscreenAvailability(),
    });
    dock = new VideoSettingsDock({
        dom,
        chrome,
        adapter: () => activeAdapter,
        state: () => currentState,
        trackPlayer: trackSwitchingPlayer,
        preferences: () => playbackPreferences,
        updatePreferences: updatePlaybackPreferences,
        hidePlaylist: () => playlist.hidePanel(),
        closeOpenMenu: () => { closeOpenMenu(); },
        syncPanelGeometry: syncActivePanelGeometry,
        syncViewportInsets: () => geometry?.syncFallbackNativeViewportInsets(),
        scheduleNativeResize: () => geometry?.scheduleNativeResize(),
        applyPicture: applyHtmlPicture,
    });
    input = new VideoInputController({
        dom,
        chrome,
        transport: () => transport,
        geometry: () => geometry,
        adapter: () => activeAdapter,
        state: () => currentState,
        native: () => activeNative,
        isOpen,
        hasError,
        isAnyMenuOpen,
        toggleSubtitles: () => dock?.subtitles.toggle(),
        close: () => { void closeVideoModal(); },
    });

    videoSetupComplete = true;
    a11y = installModalA11y(modalEl, {
        requestClose: () => {
            if (playlist.isPanelOpen) {
                hideVideoPlaylist(true);
                return;
            }
            // Escape peels one layer at a time: the dock first, the player last.
            const panel = dock?.panel;
            if (panel && !panel.hidden) {
                closeOpenMenu();
                if (!panel.hidden) dock?.hidePanel(true);
                dock?.focusAnchor();
                return;
            }
            void closeVideoModal();
        },
        initialFocus: () => dom.playButton || dom.closeButton,
        restoreFocus: "#file-list",
    });
    bindEncryptedMediaLifecycle();
    nativeStateRouter.bind();
    transport.bind();
    dock.bind();
    playlist.bindButton();
    unbindVideoDOM = bindVideoDOM(dom, {
        close: () => { void closeVideoModal(); },
        retry: () => errors?.activatePrimary(),
        toggleFullscreen: () => { void geometry?.toggleFullscreen(); },
        pointerMove: input.handlePointerMove,
        stageClick: input.handleStageClick,
        stageDoubleClick: input.handleStageDoubleClick,
        keydown: input.handleKeydown,
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
        stageEl.addEventListener("pointerdown", input.trackStagePointer);
        unbindTouchGestures = bindTouchGestures(stageEl, input.touchHandlers());
    }
    applyState(EMPTY_PLAYER_STATE);
    return teardownVideoModal;
}
