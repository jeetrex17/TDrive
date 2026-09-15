import { describe, expect, it } from 'vitest';
import {
    DEFAULT_THEME_PREFERENCE,
    THEME_DEFINITIONS,
    isThemeMode,
    normalizeThemePreference,
    resolveThemeId,
    themesForAppearance,
} from './theme-model';

describe('theme model', () => {
    it('ships a balanced, uniquely identified set of light and dark themes', () => {
        const ids = THEME_DEFINITIONS.map((theme) => theme.id);

        expect(new Set(ids).size).toBe(ids.length);
        expect(themesForAppearance('light').length).toBeGreaterThanOrEqual(5);
        expect(themesForAppearance('dark').length).toBeGreaterThanOrEqual(6);
        expect(THEME_DEFINITIONS.every((theme) => theme.preview.length === 4)).toBe(true);
        expect(THEME_DEFINITIONS.every((theme) => !('description' in theme))).toBe(true);
        expect(THEME_DEFINITIONS.find((theme) => theme.id === 'daybreak')).toMatchObject({
            name: 'Daybreak',
            appearance: 'light',
        });
        expect(THEME_DEFINITIONS.find((theme) => theme.id === 'porcelain')).toMatchObject({
            name: 'Porcelain',
            appearance: 'light',
        });
        expect(THEME_DEFINITIONS.find((theme) => theme.id === 'quiet-relay')).toMatchObject({
            name: 'Quiet Relay',
            appearance: 'dark',
        });

        const previousThemeIds = [
            'porcelain',
            'catppuccin-latte',
            'solarized-light',
            'gruvbox-light',
            'tokyo-night',
            'catppuccin-mocha',
            'dracula',
            'solarized-dark',
            'gruvbox-dark',
            'nord',
        ];
        expect(ids).toEqual(expect.arrayContaining(previousThemeIds));
    });

    it('defaults unknown or appearance-incompatible persisted values to the TDrive pair', () => {
        expect(DEFAULT_THEME_PREFERENCE).toEqual({
            mode: 'dark',
            lightThemeId: 'daybreak',
            darkThemeId: 'quiet-relay',
        });
        expect(
            normalizeThemePreference({
                mode: 'neon',
                lightThemeId: 'dracula',
                darkThemeId: 'missing-theme',
            }),
        ).toEqual(DEFAULT_THEME_PREFERENCE);
    });

    it('preserves the previous Porcelain and Tokyo Night stored preference IDs', () => {
        const persisted = {
            mode: 'dark',
            lightThemeId: 'porcelain',
            darkThemeId: 'tokyo-night',
        } as const;

        expect(normalizeThemePreference(persisted)).toEqual(persisted);
    });

    it('migrates the removed System mode to explicit dark without losing valid palettes', () => {
        expect(
            normalizeThemePreference({
                mode: 'system',
                lightThemeId: 'catppuccin-latte',
                darkThemeId: 'nord',
            }),
        ).toEqual({
            mode: 'dark',
            lightThemeId: 'catppuccin-latte',
            darkThemeId: 'nord',
        });
        expect(isThemeMode('system')).toBe(false);
    });

    it('preserves valid preferences without mutating the source value', () => {
        const persisted = {
            mode: 'light',
            lightThemeId: 'catppuccin-latte',
            darkThemeId: 'catppuccin-mocha',
        } as const;

        const normalized = normalizeThemePreference(persisted);

        expect(normalized).toEqual(persisted);
        expect(normalized).not.toBe(persisted);
    });

    it('resolves the selected palette from explicit light or dark mode', () => {
        const preference = {
            mode: 'light',
            lightThemeId: 'solarized-light',
            darkThemeId: 'nord',
        } as const;

        expect(resolveThemeId(preference)).toBe('solarized-light');
        expect(resolveThemeId({ ...preference, mode: 'dark' })).toBe('nord');
    });
});
