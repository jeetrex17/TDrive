import {
    enterFullscreen,
    exitFullscreen,
    fullscreenAvailable,
    isFullscreen,
    resizeNativeMedia,
    type NativeMediaOpenResult,
    type NativeMediaRect,
} from '../../api';
import { setNativeVideoLayerActive } from './native-video-layer';
import type { VideoDOM } from './video-dom';

export type NativeLayout = 'none' | 'embedded-overlay' | 'embedded-fallback' | 'standalone';

interface VideoGeometryContext {
    dom: VideoDOM;
    getNative(): NativeMediaOpenResult | null;
    hasActivePlayer(): boolean;
    hasError(): boolean;
    isOpen(): boolean;
    isSettingsOpen(): boolean;
    settingsPanel(): HTMLElement | null;
    revealChrome(): void;
    reportSurfaceError(message: string): void;
}

const FALLBACK_NATIVE_GAP_PX = 4;
const FALLBACK_NATIVE_SIDE_PX = 0;
const FALLBACK_NATIVE_SIDE_COMPACT_PX = 0;

export function nativeSeekOverlayAvailable(): boolean {
    // The native overlay is implemented by the Windows child-window player.
    // Linux/X11 keeps a timestamp inside the reserved HTML controls instead.
    return /windows/i.test(window.navigator.userAgent);
}

function shouldMeasureNativeFallbackBeforeOpen(): boolean {
    // macOS renders libmpv below a transparent WebView. Windows/Linux use a
    // child window above it, so reserve HTML-owned strips before opening mpv.
    return !/macintosh|mac os x/i.test(window.navigator.userAgent);
}

function nextFrame(): Promise<void> {
    return new Promise((resolve) => requestAnimationFrame(() => resolve()));
}

export class VideoGeometryController {
    private isWindowFullscreen = false;
    private nativeResizeFrame = 0;

    constructor(private readonly context: VideoGeometryContext) {}

    setNativeLayout(layout: NativeLayout): void {
        const { dom } = this.context;
        const visible = layout !== 'none';
        const overlay = layout === 'embedded-overlay';
        const fallback = layout === 'embedded-fallback';
        const standalone = layout === 'standalone';
        dom.modal?.classList.toggle('is-video-native', visible);
        dom.modal?.classList.toggle('is-video-native-fallback', fallback);
        dom.modal?.classList.toggle('is-video-native-standalone', standalone);
        dom.modal?.classList.toggle('has-native-seek-overlay', fallback && nativeSeekOverlayAvailable());
        if (dom.standalone) dom.standalone.hidden = !standalone;
        // Only the overlay layout renders mpv under the WebView. Child-window
        // fallbacks sit above it and must keep the document canvas opaque.
        setNativeVideoLayerActive(document, overlay);
        this.syncFallbackNativeViewportInsets();
    }

    refreshFullscreenAvailability(): void {
        this.applyFullscreenState(this.isWindowFullscreen);
    }

    async syncFullscreenState(): Promise<void> {
        this.applyFullscreenState(await this.readWindowFullscreen());
    }

    async exitVideoFullscreen(): Promise<void> {
        if (!fullscreenAvailable() || !(await this.readWindowFullscreen())) return;
        try {
            exitFullscreen();
            this.applyFullscreenState(false);
        } catch (error) {
            console.warn('WindowUnfullscreen failed:', error);
        }
    }

    async toggleFullscreen(): Promise<void> {
        if (!this.canUseFullscreen()) return;
        try {
            const next = !(await this.readWindowFullscreen());
            if (next) {
                enterFullscreen();
            } else {
                exitFullscreen();
            }
            this.applyFullscreenState(next);
        } catch (error) {
            console.warn('toggle fullscreen failed:', error);
        } finally {
            this.scheduleNativeResizeAfterWindowTransition();
            setTimeout(() => {
                void this.syncFullscreenState();
                this.scheduleNativeResizeAfterWindowTransition();
            }, 180);
            this.context.revealChrome();
        }
    }

    async prepareNativeRect(isCurrent: () => boolean): Promise<NativeMediaRect | null> {
        const measureFallback = shouldMeasureNativeFallbackBeforeOpen();
        this.setNativeLayout(measureFallback ? 'embedded-fallback' : 'none');
        await nextFrame();
        if (!isCurrent() || !this.context.isOpen()) return null;
        const rect = this.currentNativeRect();
        if (rect) return rect;
        this.setNativeLayout('none');
        this.context.reportSurfaceError('Could not prepare the native video surface.');
        return null;
    }

    syncFallbackNativeViewportInsets(): void {
        const { dom } = this.context;
        if (!dom.modal || !dom.stage || !dom.nativeViewport || !dom.modal.classList.contains('is-video-native-fallback')) return;
        const stageRect = dom.stage.getBoundingClientRect();
        if (stageRect.width <= 0 || stageRect.height <= 0) return;

        const topbarRect = dom.topbar?.getBoundingClientRect();
        const controlsRect = dom.controls?.getBoundingClientRect();
        const compact = window.matchMedia('(max-width: 760px)').matches;
        const side = compact ? FALLBACK_NATIVE_SIDE_COMPACT_PX : FALLBACK_NATIVE_SIDE_PX;
        const topbarBottom = topbarRect ? Math.max(topbarRect.bottom, stageRect.top) : stageRect.top;
        const controlsTop = controlsRect ? Math.min(controlsRect.top, stageRect.bottom) : stageRect.bottom;
        const top = Math.ceil(Math.max(0, topbarBottom - stageRect.top) + FALLBACK_NATIVE_GAP_PX);
        const bottom = Math.ceil(Math.max(0, stageRect.bottom - controlsTop) + FALLBACK_NATIVE_GAP_PX);

        const panelRect = this.context.isSettingsOpen()
            ? this.context.settingsPanel()?.getBoundingClientRect()
            : null;
        const panelWidth = panelRect && !compact
            ? Math.max(0, stageRect.right - panelRect.left + FALLBACK_NATIVE_GAP_PX)
            : side;
        const panelBottom = panelRect && compact
            ? Math.max(bottom, stageRect.bottom - panelRect.top + FALLBACK_NATIVE_GAP_PX)
            : bottom;
        dom.nativeViewport.style.setProperty('--video-native-right-inset', `${panelWidth}px`);
        dom.nativeViewport.style.setProperty('--video-native-side-inset', `${side}px`);
        dom.nativeViewport.style.setProperty('--video-native-top-inset', `${top}px`);
        dom.nativeViewport.style.setProperty('--video-native-bottom-inset', `${panelBottom}px`);
    }

    scheduleNativeResize(): void {
        const active = this.context.getNative();
        if (!active || active.presentation === 'standalone') return;
        if (this.nativeResizeFrame) cancelAnimationFrame(this.nativeResizeFrame);
        this.nativeResizeFrame = requestAnimationFrame(() => {
            this.nativeResizeFrame = 0;
            const current = this.context.getNative();
            if (!current || current.presentation === 'standalone') return;
            const rect = this.currentNativeRect();
            if (!rect) return;
            void resizeNativeMedia(current.token, rect).catch((error) => {
                console.warn('ResizeNativeMedia failed:', error);
            });
        });
    }

    scheduleNativeResizeAfterWindowTransition(): void {
        const active = this.context.getNative();
        if (!active || active.presentation === 'standalone') return;
        this.scheduleNativeResize();
        requestAnimationFrame(() => this.scheduleNativeResize());
        window.setTimeout(() => this.scheduleNativeResize(), 180);
        window.setTimeout(() => this.scheduleNativeResize(), 420);
    }

    observeControlsSize(layoutChanged: () => void): void {
        const controls = this.context.dom.controls;
        if (!controls || typeof ResizeObserver === 'undefined') return;
        let previousWidth = -1;
        let previousHeight = -1;
        // Controls persist for the module lifetime, across playback sessions.
        const observer = new ResizeObserver(() => {
            if (!this.context.isOpen()) return;
            const { width, height } = controls.getBoundingClientRect();
            if (width === previousWidth && height === previousHeight) return;
            previousWidth = width;
            previousHeight = height;
            layoutChanged();
            this.scheduleNativeResize();
        });
        observer.observe(controls);
    }

    handleWindowResize(layoutChanged: () => void): void {
        layoutChanged();
        if (this.context.getNative()?.presentation !== 'standalone') this.scheduleNativeResize();
        void this.syncFullscreenState();
    }

    private canUseFullscreen(): boolean {
        return Boolean(
            fullscreenAvailable()
            && this.context.hasActivePlayer()
            && this.context.getNative()?.presentation !== 'standalone'
            && !this.context.hasError()
        );
    }

    private applyFullscreenState(isFullscreenNow: boolean): void {
        this.isWindowFullscreen = isFullscreenNow;
        const { dom } = this.context;
        dom.modal?.classList.toggle('is-video-fullscreen', this.isWindowFullscreen);
        if (!dom.fullscreenButton) return;
        dom.fullscreenButton.dataset.state = this.isWindowFullscreen ? 'fullscreen' : 'windowed';
        dom.fullscreenButton.setAttribute('aria-label', this.isWindowFullscreen ? 'Exit fullscreen' : 'Enter fullscreen');
        dom.fullscreenButton.title = this.isWindowFullscreen ? 'Exit fullscreen' : 'Enter fullscreen';
        dom.fullscreenButton.disabled = !this.canUseFullscreen();
        dom.fullscreenButton.setAttribute('aria-disabled', dom.fullscreenButton.disabled ? 'true' : 'false');
    }

    private async readWindowFullscreen(): Promise<boolean> {
        if (!fullscreenAvailable()) return false;
        try {
            return await isFullscreen();
        } catch (error) {
            console.warn('WindowIsFullscreen failed:', error);
            return false;
        }
    }

    private currentNativeRect(): NativeMediaRect | null {
        this.syncFallbackNativeViewportInsets();
        const source = this.context.dom.nativeViewport || this.context.dom.stage;
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
}
