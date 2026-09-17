/**
 * The auto-hiding player chrome.
 *
 * Every surface in the player wants to keep the controls on screen for its own
 * reason — an open popover, a dragged scrubber, a paused video, a raised error —
 * so the hide timer is owned here and the reasons are asked for through the
 * context rather than reached for. That keeps the "may I hide now?" rule in one
 * place; the rule is re-checked when the timer fires because any of those
 * reasons can appear during the delay.
 */

const CHROME_HIDE_DELAY_MS = 2500;

const VISIBLE_CLASS = 'is-video-chrome-visible';
const CURSOR_HIDDEN_CLASS = 'is-video-cursor-hidden';

export interface VideoChromeContext {
    modal(): HTMLElement | null;
    isOpen(): boolean;
    isPaused(): boolean;
    hasError(): boolean;
    /** True while a popover or a live scrubber tooltip is holding the chrome open. */
    isHeldOpen(): boolean;
    /**
     * A native surface painted behind the webview swallows the cursor, so the
     * cursor is never hidden over it — there would be nothing to move it back.
     */
    isNativeFallbackActive(): boolean;
}

export class VideoChromeController {
    private hideTimer: ReturnType<typeof setTimeout> | null = null;

    constructor(private readonly ctx: VideoChromeContext) {}

    isVisible(): boolean {
        return Boolean(this.ctx.modal()?.classList.contains(VISIBLE_CLASS));
    }

    setVisible(visible: boolean): void {
        const modal = this.ctx.modal();
        modal?.classList.toggle(VISIBLE_CLASS, visible);
        modal?.classList.toggle(
            CURSOR_HIDDEN_CLASS,
            !visible && !this.ctx.hasError() && !this.ctx.isNativeFallbackActive(),
        );
    }

    clearTimer(): void {
        if (!this.hideTimer) return;
        clearTimeout(this.hideTimer);
        this.hideTimer = null;
    }

    scheduleHide(): void {
        this.clearTimer();
        if (!this.canHide()) return;
        this.hideTimer = setTimeout(() => {
            if (!this.canHide()) return;
            this.setVisible(false);
        }, CHROME_HIDE_DELAY_MS);
    }

    reveal(): void {
        if (!this.ctx.isOpen()) return;
        this.setVisible(true);
        this.scheduleHide();
    }

    /**
     * What a popover does on its way out: hand the chrome back to the timer, but
     * only if nothing else is already pinning it open.
     */
    settleAfterMenuClose(): void {
        if (this.ctx.isOpen() && !this.ctx.isPaused() && !this.ctx.hasError()) this.scheduleHide();
    }

    /** Pin the chrome open for as long as the caller's surface needs it. */
    pin(): void {
        this.setVisible(true);
        this.clearTimer();
    }

    private canHide(): boolean {
        return this.ctx.isOpen()
            && !this.ctx.isPaused()
            && !this.ctx.hasError()
            && !this.ctx.isHeldOpen();
    }
}
