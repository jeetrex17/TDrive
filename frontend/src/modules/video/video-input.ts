/**
 * Keyboard, pointer and touch input for the player shell.
 *
 * The player is a full-screen surface covered in its own focusable controls, so
 * most of the work here is deciding what is NOT the player's to act on: a
 * keystroke aimed at a slider, a form field or a panel belongs to that control,
 * and a click that began on the chrome belongs to the chrome. Everything this
 * layer keeps is dispatched straight to the transport or the geometry
 * controller — it holds no playback state of its own.
 */

import { isMobilePlatform, type NativeMediaOpenResult } from '../../api';
import type { TouchGestureHandlers } from '../../ui/preview/touch-gestures';
import type { PlayerAdapter, PlayerState } from './player-adapters';
import type { VideoChromeController } from './video-chrome';
import { byID, type VideoDOM } from './video-dom';
import type { VideoGeometryController } from './video-geometry';
import { SEEK_STEP_SECONDS, VOLUME_STEP, type VideoTransportController } from './video-transport';

// Surfaces that sit on top of the picture. A pointer event that starts on one of
// them is that surface's, not a request to play or pause.
const CHROME_SELECTOR = '.video-topbar, .video-controls, .video-center-controls, .video-error, .video-loading, .video-settings-panel, .video-playlist-panel';

const SHELL_ID = 'video-shell';

// Past these a downward throw is a dismissal rather than an accidental drag.
const SWIPE_CLOSE_PX = 120;
const SWIPE_CLOSE_FLICK_PX = 32;
const SWIPE_CLOSE_VELOCITY = 0.6;
const SWIPE_FADE_DISTANCE_PX = 480;
const SWIPE_MIN_OPACITY = 0.4;

export interface VideoInputContext {
    dom: VideoDOM;
    chrome: VideoChromeController;
    transport(): VideoTransportController | null;
    geometry(): VideoGeometryController | null;
    adapter(): PlayerAdapter | null;
    state(): PlayerState;
    native(): NativeMediaOpenResult | null;
    isOpen(): boolean;
    hasError(): boolean;
    /** A gesture must not fight a popover that is already on screen. */
    isAnyMenuOpen(): boolean;
    toggleSubtitles(): void;
    close(): void;
}

export class VideoInputController {
    // Where the last stage pointer came from: touch taps go through the phone
    // recogniser, and a tap that began on the chrome is the chrome's to handle.
    private pointerWasTouch = false;
    private pointerOnChrome = false;

    constructor(private readonly ctx: VideoInputContext) {}

    handleKeydown = (event: KeyboardEvent): void => {
        if (!this.ctx.isOpen()) return;
        // A standalone native window has the keyboard and its own controls.
        if (this.isStandalone()) return;
        const target = event.target instanceof HTMLElement ? event.target : null;
        if (targetShouldUseOwnKeyboard(target, event)) return;

        const transport = this.ctx.transport();
        const key = event.key.toLowerCase();
        if (event.code === 'Space' || event.key === ' ' || key === 'k') {
            event.preventDefault();
            transport?.togglePlayback();
        } else if (event.key === 'ArrowLeft' || key === 'j') {
            event.preventDefault();
            transport?.seekBy(-SEEK_STEP_SECONDS);
        } else if (event.key === 'ArrowRight' || key === 'l') {
            event.preventDefault();
            transport?.seekBy(SEEK_STEP_SECONDS);
        } else if (event.key === 'ArrowUp') {
            event.preventDefault();
            this.ctx.adapter()?.setVolume(this.ctx.state().volume + VOLUME_STEP);
            this.ctx.chrome.reveal();
        } else if (event.key === 'ArrowDown') {
            event.preventDefault();
            this.ctx.adapter()?.setVolume(this.ctx.state().volume - VOLUME_STEP);
            this.ctx.chrome.reveal();
        } else if (key === 'm') {
            event.preventDefault();
            this.ctx.adapter()?.setMuted(!this.ctx.state().muted);
            this.ctx.chrome.reveal();
        } else if (key === 'f') {
            event.preventDefault();
            void this.ctx.geometry()?.toggleFullscreen();
        } else if (key === 'c') {
            event.preventDefault();
            this.ctx.toggleSubtitles();
        }
    };

    handlePointerMove = (event: PointerEvent): void => {
        // A finger dragging across the phone player is a gesture, not a request
        // for the controls.
        if (event.pointerType === 'touch' && isMobilePlatform()) return;
        this.ctx.chrome.reveal();
    };

    handleStageClick = (event: MouseEvent): void => {
        if (this.ignoreStagePointer(event)) return;
        this.ctx.transport()?.togglePlayback();
    };

    handleStageDoubleClick = (event: MouseEvent): void => {
        if (this.ignoreStagePointer(event)) return;
        event.preventDefault();
        void this.ctx.geometry()?.toggleFullscreen();
    };

    trackStagePointer = (event: PointerEvent): void => {
        this.pointerWasTouch = event.pointerType === 'touch';
        this.pointerOnChrome = targetIsVideoChrome(event.target);
    };

    /**
     * Phone gestures: a tap toggles the controls, a double tap on either side
     * seeks ten seconds (in the middle it toggles playback) and a swipe down
     * closes the player.
     */
    touchHandlers(): TouchGestureHandlers {
        const shell = () => byID<HTMLElement>(SHELL_ID);
        return {
            tap: () => {
                if (!this.canGesture()) return;
                if (this.ctx.chrome.isVisible()) {
                    this.ctx.chrome.clearTimer();
                    this.ctx.chrome.setVisible(false);
                } else {
                    this.ctx.chrome.reveal();
                }
            },
            doubleTap: (x) => {
                const stage = this.ctx.dom.stage;
                if (!this.canGesture() || !stage) return;
                const { left, width } = stage.getBoundingClientRect();
                const across = (x - left) / Math.max(1, width);
                const transport = this.ctx.transport();
                if (across < 1 / 3) transport?.seekBy(-SEEK_STEP_SECONDS);
                else if (across > 2 / 3) transport?.seekBy(SEEK_STEP_SECONDS);
                else transport?.togglePlayback();
            },
            dragStart: (axis) => axis === 'y' && this.ctx.isOpen() && !this.pointerOnChrome && !this.ctx.isAnyMenuOpen(),
            drag: (_dx, dy) => {
                const el = shell();
                if (!el) return;
                const drop = Math.max(0, dy);
                el.style.transition = 'none';
                el.style.transform = `translate3d(0, ${drop}px, 0)`;
                el.style.opacity = String(Math.max(SWIPE_MIN_OPACITY, 1 - drop / SWIPE_FADE_DISTANCE_PX));
            },
            dragEnd: (_dx, dy, _axis, velocity) => {
                const el = shell();
                if (!el) return;
                el.style.transition = '';
                el.style.transform = '';
                el.style.opacity = '';
                if (dy > SWIPE_CLOSE_PX || (dy > SWIPE_CLOSE_FLICK_PX && velocity > SWIPE_CLOSE_VELOCITY)) this.ctx.close();
            },
        };
    }

    private canGesture(): boolean {
        return this.ctx.isOpen() && !this.ctx.hasError() && !this.pointerOnChrome;
    }

    private isStandalone(): boolean {
        return this.ctx.native()?.presentation === 'standalone';
    }

    private ignoreStagePointer(event: MouseEvent): boolean {
        if (this.isStandalone()) return true;
        // A touch tap already went through the phone recogniser; the click the
        // browser synthesises for it must not act a second time.
        if (this.pointerWasTouch && isMobilePlatform()) return true;
        return targetIsVideoChrome(event.target);
    }
}

export function targetIsVideoChrome(target: EventTarget | null): boolean {
    const el = target instanceof Element ? target : null;
    return Boolean(el?.closest(CHROME_SELECTOR));
}

/**
 * Whether a control gets to keep the key it was given. Buttons inside the player
 * keep only Enter and Space — the arrow keys stay with the player so a reader who
 * tabbed to Pause can still seek — while buttons elsewhere, panels, sliders and
 * text fields keep everything they would normally handle.
 */
export function targetShouldUseOwnKeyboard(target: HTMLElement | null, event: KeyboardEvent): boolean {
    if (!(target instanceof HTMLElement)) return false;
    if (target.isContentEditable || target.closest('#video-settings-panel, #video-playlist-panel')) return true;
    const tag = String(target.tagName || '').toUpperCase();
    if (tag === 'BUTTON') {
        if (target.closest('#video-speed-menu')) return true;
        if (target.closest('#video-modal')) {
            return event.key === 'Enter' || event.code === 'Space' || event.key === ' ';
        }
        return true;
    }
    if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return true;
    if (target.closest('#video-speed-menu')) return true;
    if (target.closest('#video-scrubber')) {
        return ['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key);
    }
    if (target.closest('#video-volume-slider')) {
        return ['ArrowLeft', 'ArrowDown', 'ArrowRight', 'ArrowUp', 'Home', 'End'].includes(event.key);
    }
    return false;
}
