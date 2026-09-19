import { afterEach, describe, expect, it } from 'vitest';
import {
    RATE_OPTIONS,
    formatRate,
    nextPresetRate,
    parseCustomPlaybackRate,
    speedMenuMarkup,
    syncSpeedControls,
} from './speed-menu-view';

afterEach(() => {
    document.body.replaceChildren();
});

function mountMenu() {
    const button = document.createElement('button');
    const menu = document.createElement('div');
    menu.id = 'video-speed-menu';
    menu.innerHTML = speedMenuMarkup();
    document.body.append(button, menu);
    return { button, menu };
}

describe('reading a playback rate', () => {
    it('shows a whole rate without a decimal point and trims a float round-trip', () => {
        expect(formatRate(1)).toBe('1');
        expect(formatRate(2)).toBe('2');
        expect(formatRate(1.25)).toBe('1.25');
        expect(formatRate(1.5)).toBe('1.5');
        expect(formatRate(0.75)).toBe('0.75');
    });
});

describe('stepping through the speed presets', () => {
    it('advances to the next preset up', () => {
        expect(nextPresetRate(1)).toBe(1.25);
        expect(nextPresetRate(0.5)).toBe(0.75);
    });

    it('wraps to the slowest preset from the top, so the pill always does something', () => {
        expect(nextPresetRate(2)).toBe(0.5);
        expect(nextPresetRate(4)).toBe(0.5);
    });

    it('never lands on the rate it started from after a float round-trip', () => {
        expect(nextPresetRate(1.2500000000000002)).toBe(1.5);
    });

    it('picks the next preset above a rate the slider set, which is not a preset at all', () => {
        expect(nextPresetRate(1.35)).toBe(1.5);
    });
});

describe('a typed custom speed', () => {
    it('accepts a plain number, with or without surrounding space', () => {
        expect(parseCustomPlaybackRate('1.75')).toBe(1.75);
        expect(parseCustomPlaybackRate('  2 ')).toBe(2);
    });

    it('rejects anything that is not a speed, so the field can keep focus instead of guessing', () => {
        expect(parseCustomPlaybackRate('')).toBeNull();
        expect(parseCustomPlaybackRate('fast')).toBeNull();
        expect(parseCustomPlaybackRate('0')).toBeNull();
        expect(parseCustomPlaybackRate('-2')).toBeNull();
    });

    it('clamps a rate past the ends of the supported range rather than refusing it', () => {
        expect(parseCustomPlaybackRate('99')).toBe(4);
        expect(parseCustomPlaybackRate('0.01')).toBe(0.25);
    });
});

describe('the speed menu markup', () => {
    it('renders every preset as a radio the keyboard handler can walk', () => {
        const { menu } = mountMenu();
        const options = Array.from(menu.querySelectorAll<HTMLButtonElement>('[data-rate]'));
        expect(options.map((option) => Number(option.dataset.rate))).toEqual(RATE_OPTIONS);
    });

    it('carries the slider and the custom field the controller binds to', () => {
        mountMenu();
        expect(document.getElementById('video-speed-slider')).not.toBeNull();
        expect(document.getElementById('video-speed-custom-input')).not.toBeNull();
        expect(document.getElementById('video-speed-value')).not.toBeNull();
    });
});

describe('showing the current rate on the controls', () => {
    it('puts the rate on the pill, the slider, the readout and the selected preset at once', () => {
        const { button, menu } = mountMenu();
        syncSpeedControls(button, menu, 1.5);

        expect(button.textContent).toBe('1.5x');
        expect(button.getAttribute('aria-label')).toBe(button.title);
        expect((document.getElementById('video-speed-slider') as HTMLInputElement).value).toBe('1.5');
        expect(document.getElementById('video-speed-value')?.textContent).toBe('1.5x');
        expect(menu.querySelector('[data-rate="1.5"]')?.classList.contains('is-selected')).toBe(true);
        expect(menu.querySelector('[data-rate="1"]')?.getAttribute('aria-checked')).toBe('false');
    });

    it('matches a preset through float drift rather than leaving nothing selected', () => {
        const { button, menu } = mountMenu();
        syncSpeedControls(button, menu, 1.2500000000000002);
        expect(menu.querySelector('[data-rate="1.25"]')?.classList.contains('is-selected')).toBe(true);
    });

    it('selects no preset for a rate the slider set between them', () => {
        const { button, menu } = mountMenu();
        syncSpeedControls(button, menu, 1.35);
        expect(menu.querySelectorAll('.is-selected')).toHaveLength(0);
    });

    it('leaves the custom field alone while it has focus, so a half-typed value survives', () => {
        const { button, menu } = mountMenu();
        const custom = document.getElementById('video-speed-custom-input') as HTMLInputElement;
        custom.value = '1.3';
        custom.focus();
        syncSpeedControls(button, menu, 2);
        expect(custom.value).toBe('1.3');
    });

    it('writes the rate into the custom field when the reader is not typing in it', () => {
        const { button, menu } = mountMenu();
        syncSpeedControls(button, menu, 2);
        expect((document.getElementById('video-speed-custom-input') as HTMLInputElement).value).toBe('2');
    });

    it('does nothing without a pill or a menu, so a torn-down player cannot throw', () => {
        expect(() => syncSpeedControls(null, null, 1)).not.toThrow();
    });
});
