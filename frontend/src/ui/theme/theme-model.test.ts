import { describe, expect, it } from 'vitest';
import {
    DEFAULT_THEME_PREFERENCE,
    THEME_DEFINITIONS,
    normalizeThemePreference,
    resolveThemeId,
    themesForAppearance,
} from './theme-model';

describe('theme model', () => {
    it('ships a balanced, uniquely identified set of light and dark themes', () => {
        const ids = THEME_DEFINITIONS.map((theme) => theme.id);

        expect(new Set(ids).size).toBe(ids.length);
        expect(themesForAppearance('light').length).toBeGreaterThanOrEqual(4);
        expect(themesForAppearance('dark').length).toBeGreaterThanOrEqual(5);
        expect(THEME_DEFINITIONS.every((theme) => theme.preview.length === 4)).toBe(true);
    });

    it('normalizes unknown or appearance-incompatible persisted values', () => {
        expect(
            normalizeThemePreference({
                mode: 'neon',
                lightThemeId: 'dracula',
                darkThemeId: 'missing-theme',
            }),
        ).toEqual(DEFAULT_THEME_PREFERENCE);
    });

    it('preserves valid preferences without mutating the source value', () => {
        const persisted = {
            mode: 'system',
            lightThemeId: 'catppuccin-latte',
            darkThemeId: 'catppuccin-mocha',
        } as const;

        const normalized = normalizeThemePreference(persisted);

        expect(normalized).toEqual(persisted);
        expect(normalized).not.toBe(persisted);
    });

    it('resolves system mode from the operating-system appearance', () => {
        const preference = {
            mode: 'system',
            lightThemeId: 'solarized-light',
            darkThemeId: 'nord',
        } as const;

        expect(resolveThemeId(preference, 'light')).toBe('solarized-light');
        expect(resolveThemeId(preference, 'dark')).toBe('nord');
    });

    it('lets an explicit light or dark mode override the operating system', () => {
        const preference = {
            mode: 'light',
            lightThemeId: 'gruvbox-light',
            darkThemeId: 'dracula',
        } as const;

        expect(resolveThemeId(preference, 'dark')).toBe('gruvbox-light');
        expect(resolveThemeId({ ...preference, mode: 'dark' }, 'light')).toBe('dracula');
    });
});
