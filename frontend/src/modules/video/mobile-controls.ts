import { hasMobilePlayerControls, normalizeMobilePlayerState, requestMobilePlayer, type MobilePlayerRequest, type MobilePlayerState } from '../../api/mobile-player';

interface ControlsDOM {
    rotate: HTMLButtonElement | null;
    volume: HTMLButtonElement | null;
    feedback: HTMLElement | null;
}
type Request = (request: MobilePlayerRequest) => Promise<unknown>;
let nextSession = 0;

/** Owns native settings for an open player and ignores responses from closed sessions. */
export class MobileVideoControls {
    private session: string | null = null;
    private state: MobilePlayerState | null = null;
    private opened = false;
    private gesture: { kind: 'brightness' | 'volume'; initial: number; height: number; dy: number; ready: boolean; ended: boolean } | null = null;
    private queued: { operation: 'brightness' | 'volume'; value: number } | null = null;
    private writing = false;
    private feedbackTimer: ReturnType<typeof setTimeout> | undefined;
    private abort = new AbortController();

    constructor(private dom: ControlsDOM, private request: Request = requestMobilePlayer, private available = hasMobilePlayerControls()) {
        dom.rotate?.addEventListener('click', () => { void this.rotate(); }, { signal: this.abort.signal });
        dom.volume?.addEventListener('click', () => { void this.showVolume(); }, { signal: this.abort.signal });
        document.addEventListener('visibilitychange', () => {
            if (document.hidden) this.release();
            else if (this.opened) void this.begin();
        }, { signal: this.abort.signal });
        window.addEventListener('pagehide', () => this.release(), { signal: this.abort.signal });
        window.addEventListener('tdrive:player-controls-ended', (event) => {
            const detail = (event as CustomEvent<{ session?: string }>).detail;
            if (detail?.session === this.session) this.release(false);
        }, { signal: this.abort.signal });
        window.addEventListener('tdrive:player-controls-active', () => {
            if (this.opened && !document.hidden) void this.begin();
        }, { signal: this.abort.signal });
    }

    open(): void {
        this.opened = true;
        if (!document.hidden) void this.begin();
    }

    close(): void {
        this.opened = false;
        this.release();
        this.clearFeedback();
    }

    destroy(): void {
        this.close();
        this.abort.abort();
    }

    private async begin(): Promise<void> {
        if (!this.available || this.session) return;
        const session = `video-${Date.now()}-${++nextSession}`;
        this.session = session;
        try {
            const state = normalizeMobilePlayerState(await this.request({ operation: 'begin', session }));
            if (this.session !== session) return;
            this.state = state;
            this.syncControls();
        } catch (error) {
            if (this.session !== session) return;
            this.release();
            this.feedback(error instanceof Error ? error.message : 'Screen controls unavailable');
        }
    }

    private release(notifyNative = true): void {
        const session = this.session;
        this.session = null;
        this.state = null;
        this.gesture = null;
        this.queued = null;
        this.clearFeedback();
        this.syncControls();
        if (session && notifyNative) void this.request({ operation: 'end', session }).catch((error: unknown) => console.warn('Could not release native screen controls', error));
    }

    private syncControls(): void {
        if (this.dom.rotate) this.dom.rotate.hidden = !this.state;
        if (this.dom.volume) this.dom.volume.hidden = !this.state || this.state.volumeMode === 'unavailable';
    }

    /** The centre remains available for swipe-to-close. Edges own brightness/volume. */
    startDrag(across: number, height: number): boolean {
        if (!this.state || !this.session || (across >= 0.4 && across <= 0.6)) return false;
        if (across > 0.6 && this.state.volumeMode !== 'system') {
            if (this.state.volumeMode === 'native-slider') void this.showVolume();
            else this.feedback('Volume is controlled by this device');
            this.gesture = null;
            return true;
        }
        const kind: 'brightness' | 'volume' = across < 0.4 ? 'brightness' : 'volume';
        const gesture = { kind, initial: this.state[kind], height: Math.max(100, height), dy: 0, ready: false, ended: false };
        this.gesture = gesture;
        const session = this.session;
        // Hardware volume keys may have changed the native value since opening.
        void this.request({ operation: 'state', session }).then((value) => {
            if (this.session !== session || this.gesture !== gesture) return;
            const snapshot = normalizeMobilePlayerState(value);
            this.state = snapshot;
            gesture.initial = snapshot[kind];
            gesture.ready = true;
            this.drag(gesture.dy);
            if (gesture.ended) this.gesture = null;
        }).catch((error: unknown) => {
            if (this.session !== session || this.gesture !== gesture) return;
            this.gesture = null;
            this.feedback(error instanceof Error ? error.message : 'Screen control unavailable');
        });
        return true;
    }

    drag(dy: number): void {
        if (!this.gesture || !this.state) return;
        this.gesture.dy = dy;
        if (!this.gesture.ready) return;
        const { kind, initial, height } = this.gesture;
        const value = Math.max(kind === 'brightness' ? 0.05 : 0, Math.min(1, initial - dy / height));
        this.state = { ...this.state, [kind]: value };
        this.feedback(`${kind === 'brightness' ? 'Brightness' : 'Volume'} ${Math.round(value * 100)}%`);
        this.queued = { operation: kind, value };
        void this.flush();
    }

    endDrag(): void {
        if (!this.gesture) return;
        this.gesture.ended = true;
        if (this.gesture.ready) this.gesture = null;
    }

    private async flush(): Promise<void> {
        if (this.writing || !this.session) return;
        this.writing = true;
        const session = this.session;
        try {
            while (this.queued && this.session === session) {
                const update = this.queued;
                this.queued = null;
                await this.request({ ...update, session });
            }
        } catch (error) {
            if (this.session === session) this.feedback(error instanceof Error ? error.message : 'Could not change screen control');
        } finally {
            this.writing = false;
            if (this.queued && this.session) void this.flush();
        }
    }

    private async showVolume(): Promise<void> {
        if (!this.session || !this.state) return;
        const session = this.session;
        try {
            await this.request({ operation: 'showVolume', session });
            if (this.session === session && this.state?.volumeMode === 'native-slider') this.feedback('Drag the system volume slider');
        } catch (error) {
            if (this.session === session) this.feedback(error instanceof Error ? error.message : 'Volume control unavailable');
        }
    }

    private async rotate(): Promise<void> {
        if (!this.session || !this.state) return;
        const session = this.session;
        const orientation = window.innerWidth > window.innerHeight ? 'portrait' : 'landscape';
        if (this.dom.rotate) this.dom.rotate.disabled = true;
        try {
            await this.request({ operation: 'rotate', session, orientation });
        } catch (error) {
            if (this.session === session) this.feedback(error instanceof Error ? error.message : 'Could not rotate the screen');
        } finally {
            if (this.dom.rotate) this.dom.rotate.disabled = false;
        }
    }

    private feedback(message: string): void {
        this.clearFeedback();
        if (!this.dom.feedback) return;
        this.dom.feedback.textContent = message;
        this.dom.feedback.hidden = false;
        this.feedbackTimer = setTimeout(() => this.clearFeedback(), 1800);
    }

    private clearFeedback(): void {
        clearTimeout(this.feedbackTimer);
        if (this.dom.feedback) this.dom.feedback.hidden = true;
    }
}
