import { afterEach, describe, expect, it } from 'vitest';
import { trackInputModality } from './input-modality';

let stop: (() => void) | null = null;
afterEach(() => { stop?.(); stop = null; });

describe('input modality', () => {
    it('starts with no modality, then follows the last keyboard or pointer input', () => {
        stop = trackInputModality();
        const root = document.documentElement;
        expect(root.dataset.input).toBeUndefined();

        window.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown' }));
        expect(root.dataset.input).toBe('keyboard');

        window.dispatchEvent(new Event('pointerdown'));
        expect(root.dataset.input).toBe('pointer');
    });

    it('ignores typing that is not navigation', () => {
        stop = trackInputModality();
        window.dispatchEvent(new KeyboardEvent('keydown', { key: 'a' }));
        expect(document.documentElement.dataset.input).toBeUndefined();
    });
});
