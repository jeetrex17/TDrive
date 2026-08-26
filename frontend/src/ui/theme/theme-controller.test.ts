import { get } from 'svelte/store';
import { describe, expect, it } from 'vitest';
import { createThemeController } from './theme-controller';

describe('theme controller without a browser runtime', () => {
    it('preserves the dark default when an OS color-scheme signal is unavailable', () => {
        const controller = createThemeController({
            storage: { getItem: () => null, setItem: () => {} },
        });

        controller.start();

        expect(get(controller.state).systemAppearance).toBe('dark');
        expect(get(controller.state).resolvedThemeId).toBe('tokyo-night');
        controller.destroy();
    });

    it('remains usable during SSR and applies preferences without touching the DOM', () => {
        const controller = createThemeController();

        expect(() => controller.start()).not.toThrow();
        expect(() => controller.setMode('dark')).not.toThrow();
        expect(get(controller.state).preference.mode).toBe('dark');
        expect(get(controller.state).resolvedThemeId).toBe('tokyo-night');

        expect(() => controller.destroy()).not.toThrow();
    });
});
