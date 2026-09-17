/**
 * The player's failure surface.
 *
 * Raising an error ends the load, so this owns the flag the rest of the player
 * reads to know it must stop: the spinner, the stats poll and the chrome timer
 * all defer to it. It also decides what the primary button does — Retry is only
 * honest when the failure might not recur, so a failure that will repeat
 * identically forever carries an action that can actually help instead.
 */

import type { VideoChromeController } from './video-chrome';
import type { VideoDOM } from './video-dom';
import type { VideoStatusController } from './video-status';

/** The one thing an error offers the reader beyond Retry. */
export interface ErrorAction {
    label: string;
    run: () => void;
}

export function errorDetail(err: unknown): string {
    return err instanceof Error && err.message ? err.message : String(err || '');
}

/**
 * Transport-layer failures surface as gotd internals that mean nothing to a
 * reader, so the ones we recognise are restated as the situation they describe.
 */
export function errorMessage(err: unknown, fallback: string): string {
    const normalized = errorDetail(err).toLowerCase();
    if (
        normalized.includes('resolve peer') ||
        normalized.includes('rpcdorequest') ||
        normalized.includes('retryuntilack') ||
        normalized.includes('engine forcibly closed')
    ) {
        return 'Could not reach Telegram. Check your connection and try again.';
    }
    if (normalized.includes('context canceled')) {
        return 'The video request was canceled. Try again.';
    }
    return fallback;
}

export interface VideoErrorContext {
    dom: VideoDOM;
    chrome: VideoChromeController;
    status: VideoStatusController;
    isOpen(): boolean;
    /** Reopens whatever the current attempt was targeting; null when there is nothing to retry. */
    retryCurrent(): void;
}

export class VideoErrorSurface {
    private raised = false;
    private primaryAction: ErrorAction | null = null;

    constructor(private readonly ctx: VideoErrorContext) {}

    get active(): boolean {
        return this.raised;
    }

    show(message: string, primary: ErrorAction | null = null): void {
        const { error, errorMessage: messageEl, errorRetryButton, modal } = this.ctx.dom;
        // Set before the load is torn down: the spinner's debounce and the
        // chrome's cursor rule both read this flag.
        this.raised = true;
        this.primaryAction = primary;
        if (errorRetryButton) errorRetryButton.textContent = primary ? primary.label : 'Retry';
        this.ctx.status.clearOverride();
        this.ctx.status.setLoading(false);
        this.ctx.status.stopPolling();
        if (messageEl) messageEl.textContent = message;
        if (error) error.style.display = 'block';
        modal?.classList.add('is-video-error');
        this.ctx.chrome.pin();
        if (this.ctx.isOpen()) errorRetryButton?.focus({ preventScroll: true });
    }

    clear(): void {
        const { error, errorMessage: messageEl, errorRetryButton, modal, closeButton, playButton } = this.ctx.dom;
        // Focus must not be left on a button that is about to be hidden.
        const restoreFocus = Boolean(error?.contains(document.activeElement));
        this.raised = false;
        this.primaryAction = null;
        if (errorRetryButton) errorRetryButton.textContent = 'Retry';
        messageEl?.replaceChildren();
        if (error) error.style.display = 'none';
        modal?.classList.remove('is-video-error');
        if (restoreFocus && this.ctx.isOpen()) (closeButton || playButton)?.focus({ preventScroll: true });
    }

    /** What the primary button does, whichever of the two things it currently is. */
    activatePrimary(): void {
        if (!this.raised || !this.ctx.isOpen()) return;
        if (this.primaryAction) {
            const run = this.primaryAction.run;
            run();
            return;
        }
        this.ctx.retryCurrent();
    }
}
