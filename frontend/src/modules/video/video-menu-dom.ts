/**
 * Shared vocabulary for the player's popovers: the settings dock's sections, the
 * radio-item markup every list is built from, and the roving-focus keyboard
 * model they all follow.
 *
 * These are the pieces the track pickers and the speed menu would otherwise each
 * reinvent, so they live one level below both.
 */

import { clampPlaybackRate } from './player-adapters';

export const SETTINGS_SECTIONS = ['picture', 'audio', 'subtitle', 'speed'] as const;
export type SettingsSection = (typeof SETTINGS_SECTIONS)[number];

/** The presets the speed pill cycles through; the slider can land between them. */
export const RATE_OPTIONS = [0.5, 0.75, 1, 1.25, 1.5, 2];

export function escapeHTML(value: string): string {
    return value.replace(/[&<>"']/g, (char) => `&#${char.charCodeAt(0)};`);
}

export function menuItemMarkup(attributes: string, label: string): string {
    return `<button type="button" role="radio" ${attributes} aria-checked="false"><span class="video-menu-check" aria-hidden="true">✓</span><span>${escapeHTML(label)}</span></button>`;
}

/** Trailing zeros make a speed pill jitter in width, so 1.50 reads as 1.5. */
export function formatRate(rate: number): string {
    return Number.isInteger(rate) ? String(rate) : String(rate).replace(/0+$/, '').replace(/\.$/, '');
}

export function parseCustomPlaybackRate(value: string): number | null {
    const rate = Number(value.trim());
    if (!Number.isFinite(rate) || rate <= 0) return null;
    return clampPlaybackRate(rate);
}

/**
 * nextPresetRate steps to the first preset above the current rate and wraps at
 * the top, so a custom rate from the slider still lands on a sensible next step.
 */
export function nextPresetRate(current: number): number {
    return RATE_OPTIONS.find((rate) => rate > current + 0.001) ?? RATE_OPTIONS[0];
}

/**
 * A popover list is one tab stop with arrow-key roving focus, so the menu keeps
 * the keyboard rather than leaking every item into the page's tab order. The
 * caller passes only the items that are actually on screen; a hidden item must
 * not become a dead stop in the cycle.
 */
export function handleMenuKeydown(event: KeyboardEvent, buttons: HTMLButtonElement[], close: () => void): void {
    if ((event.target as HTMLElement)?.tagName === 'INPUT' && event.key === 'ArrowDown') {
        buttons[0]?.focus();
        event.preventDefault();
        event.stopPropagation();
        return;
    }
    const current = Math.max(0, buttons.indexOf(document.activeElement as HTMLButtonElement));
    const focusAt = (index: number) => buttons[(index + buttons.length) % buttons.length]?.focus({ preventScroll: true });
    switch (event.key) {
        case 'Escape':
            close();
            break;
        case 'ArrowDown':
        case 'ArrowRight':
            focusAt(current + 1);
            break;
        case 'ArrowUp':
        case 'ArrowLeft':
            focusAt(current - 1);
            break;
        case 'Home':
            focusAt(0);
            break;
        case 'End':
            focusAt(buttons.length - 1);
            break;
        case 'Enter':
        case ' ':
            (document.activeElement as HTMLButtonElement | null)?.click();
            break;
        default:
            return;
    }
    event.preventDefault();
    event.stopPropagation();
}
