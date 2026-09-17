/**
 * Playback speed: the pill that cycles presets and the dock section that offers
 * a slider, the presets and a typed value.
 *
 * The three inputs are one control with three affordances, so all of them are
 * rendered and synced here and every one of them routes through the adapter —
 * the menu never holds a rate of its own, it only reflects the player's.
 */

import {
    MAX_PLAYBACK_RATE,
    MIN_PLAYBACK_RATE,
    type PlayerAdapter,
    type PlayerState,
} from './player-adapters';
import type { VideoChromeController } from './video-chrome';
import type { VideoDOM } from './video-dom';
import {
    RATE_OPTIONS,
    formatRate,
    handleMenuKeydown,
    nextPresetRate,
    parseCustomPlaybackRate,
} from './video-menu-dom';

const SLIDER_ID = 'video-speed-slider';
const CUSTOM_INPUT_ID = 'video-speed-custom-input';
const VALUE_ID = 'video-speed-value';

export interface SpeedMenuContext {
    dom: VideoDOM;
    chrome: VideoChromeController;
    adapter(): PlayerAdapter | null;
    state(): PlayerState;
    /** Closes every other popover before this one takes the dock. */
    closeOthers(): void;
    showSection(): void;
    isSectionActive(): boolean;
    hideSection(): void;
    focusAnchor(): void;
}

export class SpeedMenuController {
    constructor(private readonly ctx: SpeedMenuContext) {}

    isOpen(): boolean {
        return Boolean(this.ctx.dom.speedMenu?.classList.contains('is-open'));
    }

    contains(target: Node | null): boolean {
        const { speedMenu, speedButton } = this.ctx.dom;
        return Boolean(target && (speedMenu?.contains(target) || speedButton?.contains(target)));
    }

    render(): void {
        const menu = this.ctx.dom.speedMenu;
        if (!menu) return;
        const options = RATE_OPTIONS.map(speedOptionMarkup).join('');
        menu.innerHTML = `<div class="video-speed-adjustment"><label class="video-settings-field" for="${SLIDER_ID}"><span>Playback speed <output id="${VALUE_ID}">1x</output></span><input id="${SLIDER_ID}" class="video-settings-range" type="range" min="${MIN_PLAYBACK_RATE}" max="${MAX_PLAYBACK_RATE}" step="0.05" value="1" aria-valuetext="1 times" /></label><div class="video-range-endpoints" aria-hidden="true"><span>${MIN_PLAYBACK_RATE}x</span><span>${MAX_PLAYBACK_RATE}x</span></div></div><div class="video-speed-presets" role="menu" aria-label="Speed presets">${options}</div>${customSpeedMarkup()}`;
    }

    bind(): void {
        const { speedButton, speedMenu } = this.ctx.dom;
        speedButton?.addEventListener('click', (event) => {
            event.stopPropagation();
            const adapter = this.ctx.adapter();
            if (!adapter) return;
            adapter.setSpeed(nextPresetRate(this.ctx.state().rate));
            this.ctx.chrome.reveal();
        });
        speedMenu?.addEventListener('click', (event) => {
            const button = (event.target as HTMLElement | null)?.closest<HTMLButtonElement>('[data-rate]');
            const adapter = this.ctx.adapter();
            if (!button || !adapter) return;
            adapter.setSpeed(Number(button.dataset.rate || 1));
            this.close(true);
            this.ctx.chrome.reveal();
        });
        speedMenu?.addEventListener('input', (event) => {
            const input = event.target;
            if (!(input instanceof HTMLInputElement) || input.id !== SLIDER_ID) return;
            const rate = parseCustomPlaybackRate(input.value);
            if (rate !== null) this.ctx.adapter()?.setSpeed(rate);
            this.ctx.chrome.reveal();
        });
        speedMenu?.addEventListener('submit', (event) => {
            event.preventDefault();
            event.stopPropagation();
            const adapter = this.ctx.adapter();
            if (!adapter) return;
            const input = this.field<HTMLInputElement>(CUSTOM_INPUT_ID);
            const rate = input ? parseCustomPlaybackRate(input.value) : null;
            // An unusable value keeps the field so it can be corrected in place.
            if (rate == null) {
                input?.focus({ preventScroll: true });
                return;
            }
            adapter.setSpeed(rate);
            this.close(true);
            this.ctx.chrome.reveal();
        });
        speedMenu?.addEventListener('keydown', (event) => {
            if (!this.isOpen()) return;
            const target = event.target as HTMLElement | null;
            // The slider and the number field own their own arrow keys; only
            // Escape is taken from them.
            if (target?.closest('.video-speed-custom, .video-speed-adjustment')) {
                if (event.key === 'Escape') {
                    event.preventDefault();
                    event.stopPropagation();
                    this.close(true);
                }
                return;
            }
            handleMenuKeydown(event, this.presetButtons(), () => this.close(true));
        });
    }

    setOpen(open: boolean): void {
        if (open) {
            this.ctx.closeOthers();
            this.ctx.showSection();
        }
        this.ctx.dom.speedMenu?.classList.toggle('is-open', open);
        if (!open && this.ctx.isSectionActive()) this.ctx.hideSection();
        if (open) {
            this.ctx.chrome.clearTimer();
            this.focusSlider();
        } else {
            this.ctx.chrome.settleAfterMenuClose();
        }
    }

    close(restoreFocus = false): void {
        if (!this.isOpen()) return;
        this.setOpen(false);
        if (restoreFocus) this.ctx.focusAnchor();
    }

    /**
     * Opens the list without re-entering the dock: the dock is already showing
     * this section, and routing back through setOpen would reopen it.
     */
    openInPlace(): void {
        this.ctx.dom.speedMenu?.classList.add('is-open');
        this.ctx.chrome.clearTimer();
        this.focusSlider();
    }

    closeInPlace(): void {
        this.ctx.dom.speedMenu?.classList.remove('is-open');
    }

    sync(state: PlayerState): void {
        const { speedButton } = this.ctx.dom;
        const label = formatRate(state.rate);
        if (speedButton) {
            speedButton.textContent = `${label}x`;
            speedButton.title = `Playback speed: ${label}x. Click to cycle`;
            speedButton.setAttribute('aria-label', speedButton.title);
        }
        // Writing into the field the reader is typing in would fight them for it.
        const customInput = this.field<HTMLInputElement>(CUSTOM_INPUT_ID);
        if (customInput && document.activeElement !== customInput) {
            customInput.value = label;
        }
        const slider = this.field<HTMLInputElement>(SLIDER_ID);
        if (slider) {
            slider.value = String(state.rate);
            slider.setAttribute('aria-valuetext', `${label} times`);
            slider.style.setProperty(
                '--range-fill',
                `${(state.rate - MIN_PLAYBACK_RATE) / (MAX_PLAYBACK_RATE - MIN_PLAYBACK_RATE) * 100}%`,
            );
        }
        const value = this.field<HTMLElement>(VALUE_ID);
        if (value) value.textContent = `${label}x`;
        for (const button of this.presetButtons()) {
            const selected = Math.abs(Number(button.dataset.rate || 1) - state.rate) < 0.001;
            button.classList.toggle('is-selected', selected);
            button.setAttribute('aria-checked', selected ? 'true' : 'false');
        }
    }

    private focusSlider(): void {
        requestAnimationFrame(() => {
            const menu = this.ctx.dom.speedMenu;
            if (this.isOpen() && !menu?.contains(document.activeElement)) {
                this.field<HTMLElement>(SLIDER_ID)?.focus({ preventScroll: true });
            }
        });
    }

    private presetButtons(): HTMLButtonElement[] {
        return Array.from(this.ctx.dom.speedMenu?.querySelectorAll<HTMLButtonElement>('[data-rate]') || []);
    }

    /** The menu renders its own fields, so they are resolved within it. */
    private field<T extends HTMLElement>(id: string): T | null {
        return this.ctx.dom.speedMenu?.querySelector<T>(`#${id}`) ?? null;
    }
}

function speedOptionMarkup(rate: number): string {
    return `<button type="button" role="menuitemradio" data-rate="${rate}" aria-checked="${rate === 1 ? 'true' : 'false'}"><span class="video-menu-check" aria-hidden="true">✓</span><span>${formatRate(rate)}x</span></button>`;
}

function customSpeedMarkup(): string {
    return `<form class="video-speed-custom" role="none" aria-label="Custom playback speed"><label for="${CUSTOM_INPUT_ID}">Custom</label><div class="video-speed-custom-row"><input id="${CUSTOM_INPUT_ID}" type="number" inputmode="decimal" min="${MIN_PLAYBACK_RATE}" max="${MAX_PLAYBACK_RATE}" step="0.05" value="1" aria-label="Custom playback speed" /><span aria-hidden="true">x</span><button type="submit">Set</button></div></form>`;
}
