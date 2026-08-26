export const THEME_IDS = [
    'tdrive-light',
    'catppuccin-latte',
    'solarized-light',
    'gruvbox-light',
    'tokyo-night',
    'catppuccin-mocha',
    'dracula',
    'solarized-dark',
    'gruvbox-dark',
    'nord',
] as const;

export type ThemeId = (typeof THEME_IDS)[number];
export type ThemeAppearance = 'light' | 'dark';
export type ThemeMode = 'system' | ThemeAppearance;
export type ThemePreview = readonly [canvas: string, surface: string, accent: string, text: string];

export interface ThemeDefinition {
    readonly id: ThemeId;
    readonly name: string;
    readonly appearance: ThemeAppearance;
    readonly description: string;
    readonly preview: ThemePreview;
}

export interface ThemePreference {
    readonly mode: ThemeMode;
    readonly lightThemeId: ThemeId;
    readonly darkThemeId: ThemeId;
}

function defineTheme(definition: ThemeDefinition): ThemeDefinition {
    return Object.freeze({
        ...definition,
        preview: Object.freeze([...definition.preview]) as ThemePreview,
    });
}

/**
 * The built-in catalogue is intentionally closed and trusted. Preview values are
 * rendered as inline CSS by the appearance picker, so arbitrary persisted data
 * must never be promoted into this list.
 */
export const THEME_DEFINITIONS: readonly ThemeDefinition[] = Object.freeze([
    defineTheme({
        id: 'tdrive-light',
        name: 'TDrive Light',
        appearance: 'light',
        description: 'Crisp, calm, and unmistakably TDrive.',
        preview: ['#f3f5f9', '#eceff5', '#315fc4', '#101828'],
    }),
    defineTheme({
        id: 'catppuccin-latte',
        name: 'Catppuccin Latte',
        appearance: 'light',
        description: 'Warm pastels with gentle contrast.',
        preview: ['#eff1f5', '#dfe3eb', '#1d61ea', '#4c4f69'],
    }),
    defineTheme({
        id: 'solarized-light',
        name: 'Solarized Light',
        appearance: 'light',
        description: 'Paper-soft tones for focused work.',
        preview: ['#fdf6e3', '#eee8d5', '#1676ad', '#073642'],
    }),
    defineTheme({
        id: 'gruvbox-light',
        name: 'Gruvbox Light',
        appearance: 'light',
        description: 'A warm retro palette with earthy depth.',
        preview: ['#fbf1c7', '#ebdbb2', '#076678', '#282828'],
    }),
    defineTheme({
        id: 'tokyo-night',
        name: 'Tokyo Night',
        appearance: 'dark',
        description: 'TDrive’s original midnight-blue glow.',
        preview: ['#1a1b26', '#24283b', '#7aa2f7', '#f5f7ff'],
    }),
    defineTheme({
        id: 'catppuccin-mocha',
        name: 'Catppuccin Mocha',
        appearance: 'dark',
        description: 'Velvety dark surfaces and soft pastels.',
        preview: ['#1e1e2e', '#313244', '#89b4fa', '#f1f3fb'],
    }),
    defineTheme({
        id: 'dracula',
        name: 'Dracula',
        appearance: 'dark',
        description: 'Vivid violet energy on charcoal.',
        preview: ['#282a36', '#383a4a', '#bd93f9', '#f8f8f2'],
    }),
    defineTheme({
        id: 'solarized-dark',
        name: 'Solarized Dark',
        appearance: 'dark',
        description: 'Low-glare contrast with oceanic accents.',
        preview: ['#002b36', '#0d414d', '#36a4d9', '#f5efdd'],
    }),
    defineTheme({
        id: 'gruvbox-dark',
        name: 'Gruvbox Dark',
        appearance: 'dark',
        description: 'Cozy earth tones built for long sessions.',
        preview: ['#282828', '#3c3836', '#83a598', '#fbf1c7'],
    }),
    defineTheme({
        id: 'nord',
        name: 'Nord',
        appearance: 'dark',
        description: 'Cool arctic hues with quiet clarity.',
        preview: ['#2e3440', '#3b4252', '#88c0d0', '#eceff4'],
    }),
]);

export const DEFAULT_THEME_PREFERENCE: ThemePreference = Object.freeze({
    mode: 'system',
    lightThemeId: 'tdrive-light',
    darkThemeId: 'tokyo-night',
});

const THEME_ID_SET: ReadonlySet<string> = new Set(THEME_IDS);
const THEME_BY_ID: ReadonlyMap<ThemeId, ThemeDefinition> = new Map(
    THEME_DEFINITIONS.map((theme) => [theme.id, theme]),
);
const THEMES_BY_APPEARANCE: Readonly<Record<ThemeAppearance, readonly ThemeDefinition[]>> = Object.freeze({
    light: Object.freeze(THEME_DEFINITIONS.filter((theme) => theme.appearance === 'light')),
    dark: Object.freeze(THEME_DEFINITIONS.filter((theme) => theme.appearance === 'dark')),
});

export function isThemeAppearance(value: unknown): value is ThemeAppearance {
    return value === 'light' || value === 'dark';
}

export function isThemeMode(value: unknown): value is ThemeMode {
    return value === 'system' || isThemeAppearance(value);
}

export function isThemeId(value: unknown): value is ThemeId {
    return typeof value === 'string' && THEME_ID_SET.has(value);
}

export function getThemeDefinition(themeId: ThemeId): ThemeDefinition {
    // Every ThemeId originates from THEME_IDS, so this lookup is exhaustive.
    return THEME_BY_ID.get(themeId) as ThemeDefinition;
}

export function themesForAppearance(appearance: ThemeAppearance): readonly ThemeDefinition[] {
    return THEMES_BY_APPEARANCE[appearance];
}

export function isThemeForAppearance(value: unknown, appearance: ThemeAppearance): value is ThemeId {
    return isThemeId(value) && getThemeDefinition(value).appearance === appearance;
}

/** Returns a fresh, validated preference suitable for application state. */
export function normalizeThemePreference(value: unknown): ThemePreference {
    const candidate = isRecord(value) ? value : {};
    const mode = isThemeMode(candidate.mode) ? candidate.mode : DEFAULT_THEME_PREFERENCE.mode;
    const lightThemeId = isThemeForAppearance(candidate.lightThemeId, 'light')
        ? candidate.lightThemeId
        : DEFAULT_THEME_PREFERENCE.lightThemeId;
    const darkThemeId = isThemeForAppearance(candidate.darkThemeId, 'dark')
        ? candidate.darkThemeId
        : DEFAULT_THEME_PREFERENCE.darkThemeId;

    return Object.freeze({ mode, lightThemeId, darkThemeId });
}

export function resolveThemeAppearance(
    preference: ThemePreference,
    systemAppearance: ThemeAppearance,
): ThemeAppearance {
    return preference.mode === 'system' ? systemAppearance : preference.mode;
}

export function resolveThemeId(
    preference: ThemePreference,
    systemAppearance: ThemeAppearance,
): ThemeId {
    return resolveThemeAppearance(preference, systemAppearance) === 'light'
        ? preference.lightThemeId
        : preference.darkThemeId;
}

function isRecord(value: unknown): value is Record<PropertyKey, unknown> {
    return typeof value === 'object' && value !== null && !Array.isArray(value);
}
