/**
 * The playback-speed menu's markup, its rate arithmetic, and the one function
 * that writes a rate back out to the controls.
 *
 * This is deliberately view-only: it never touches a player. The speed menu is
 * driven from three directions at once -- the pill cycling presets, the slider,
 * and the typed custom value -- and each of them has to end up showing the same
 * number in four places. Pulling the "what does rate R look like" half out of
 * the controller leaves the controller holding only "who asked for a new rate",
 * and makes the rounding, the preset stepping and the parsing testable without
 * a player or a menu on screen.
 */

import { MAX_PLAYBACK_RATE, MIN_PLAYBACK_RATE, clampPlaybackRate } from "./player-adapters";
import { byID } from "./video-dom";

/** The presets on the pill and in the menu, in the order the pill cycles them. */
export const RATE_OPTIONS = [0.5, 0.75, 1, 1.25, 1.5, 2];

/**
 * A rate as the reader should read it: "1", "1.25", never "1.2500000000000002".
 * Rates arrive from a 0.05-step slider and from float arithmetic, so the raw
 * number is not presentable.
 */
export function formatRate(rate: number): string {
    return Number.isInteger(rate) ? String(rate) : String(rate).replace(/0+$/, "").replace(/\.$/, "");
}

/**
 * The next preset above `current`, wrapping at the top.
 *
 * The epsilon matters because the current rate is often a preset that came back
 * through a float round-trip; without it, cycling from 1.25 can land on 1.25
 * again and the pill looks stuck. Searching for the first preset *above* the
 * current rate rather than indexing the array is what keeps the pill useful
 * after the slider has set a rate that is not a preset at all.
 */
export function nextPresetRate(current: number): number {
    return RATE_OPTIONS.find((rate) => rate > current + 0.001) ?? RATE_OPTIONS[0];
}

/**
 * Read a typed custom speed, or null when it is not a speed.
 *
 * Null is distinct from a clamped 0 on purpose: the caller keeps focus in the
 * field on null rather than silently playing at some other rate than the one
 * that was typed.
 */
export function parseCustomPlaybackRate(value: string): number | null {
    const rate = Number(value.trim());
    if (!Number.isFinite(rate) || rate <= 0) return null;
    return clampPlaybackRate(rate);
}

function speedOptionMarkup(rate: number): string {
    return `<button type="button" role="menuitemradio" data-rate="${rate}" aria-checked="${rate === 1 ? "true" : "false"}"><span class="video-menu-check" aria-hidden="true">✓</span><span>${formatRate(rate)}x</span></button>`;
}

function customSpeedMarkup(): string {
    return `<form class="video-speed-custom" role="none" aria-label="Custom playback speed"><label for="video-speed-custom-input">Custom</label><div class="video-speed-custom-row"><input id="video-speed-custom-input" type="number" inputmode="decimal" min="${MIN_PLAYBACK_RATE}" max="${MAX_PLAYBACK_RATE}" step="0.05" value="1" aria-label="Custom playback speed" /><span aria-hidden="true">x</span><button type="submit">Set</button></div></form>`;
}

/** The whole menu body: slider, presets, custom field. Written once, at setup. */
export function speedMenuMarkup(): string {
    const options = RATE_OPTIONS.map(speedOptionMarkup).join("");
    return `<div class="video-speed-adjustment"><label class="video-settings-field" for="video-speed-slider"><span>Playback speed <output id="video-speed-value">1x</output></span><input id="video-speed-slider" class="video-settings-range" type="range" min="${MIN_PLAYBACK_RATE}" max="${MAX_PLAYBACK_RATE}" step="0.05" value="1" aria-valuetext="1 times" /></label><div class="video-range-endpoints" aria-hidden="true"><span>${MIN_PLAYBACK_RATE}x</span><span>${MAX_PLAYBACK_RATE}x</span></div></div><div class="video-speed-presets" role="menu" aria-label="Speed presets">${options}</div>${customSpeedMarkup()}`;
}

/**
 * Show `rate` on the pill, the slider, the readout and the preset list.
 *
 * This runs on every player state event, so it takes the two elements it needs
 * as plain arguments rather than a context object: a per-event object literal
 * would be an allocation on a path that fires several times a second during
 * playback. The remaining lookups are by id, exactly as before, and nothing
 * here allocates beyond the strings the DOM is about to be given anyway.
 *
 * The custom field is skipped while it has focus, because overwriting a field
 * mid-keystroke is how "1.3" becomes "1" under the reader's hands.
 */
export function syncSpeedControls(
    speedButton: HTMLButtonElement | null,
    speedMenu: HTMLElement | null,
    rate: number,
): void {
    const customInput = byID<HTMLInputElement>("video-speed-custom-input");
    if (speedButton) {
        speedButton.textContent = `${formatRate(rate)}x`;
        speedButton.title = `Playback speed: ${formatRate(rate)}x. Click to cycle`;
        speedButton.setAttribute("aria-label", speedButton.title);
    }
    if (customInput && document.activeElement !== customInput) {
        customInput.value = formatRate(rate);
    }
    const slider = byID<HTMLInputElement>("video-speed-slider");
    if (slider) {
        slider.value = String(rate);
        slider.setAttribute("aria-valuetext", `${formatRate(rate)} times`);
        slider.style.setProperty("--range-fill", `${(rate - MIN_PLAYBACK_RATE) / (MAX_PLAYBACK_RATE - MIN_PLAYBACK_RATE) * 100}%`);
    }
    const value = byID("video-speed-value");
    if (value) value.textContent = `${formatRate(rate)}x`;
    speedMenu?.querySelectorAll<HTMLButtonElement>("[data-rate]").forEach((button) => {
        const selected = Math.abs(Number(button.dataset.rate || 1) - rate) < 0.001;
        button.classList.toggle("is-selected", selected);
        button.setAttribute("aria-checked", selected ? "true" : "false");
    });
}
