import { get } from 'svelte/store';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createThemeController, THEME_STORAGE_KEY } from './theme-controller';
import type { ThemeAppearance, ThemeId } from './theme-model';

type MediaChangeListener = (event: MediaQueryListEvent) => void;
let storage: Storage;

function createStorage(): Storage {
    const values = new Map<string, string>();
    return {
        get length() {
            return values.size;
        },
        clear: () => values.clear(),
        getItem: (key) => values.get(key) ?? null,
        key: (index) => Array.from(values.keys())[index] ?? null,
        removeItem: (key) => values.delete(key),
        setItem: (key, value) => values.set(key, value),
    };
}

function createMediaQuery(initialMatches: boolean): MediaQueryList & { setMatches(value: boolean): void } {
    let matches = initialMatches;
    const listeners = new Set<MediaChangeListener>();

    return {
        media: '(prefers-reduced-motion: reduce)',
        onchange: null,
        get matches() {
            return matches;
        },
        addEventListener: (_type: string, listener: EventListenerOrEventListenerObject) =>
            listeners.add(listener as MediaChangeListener),
        removeEventListener: (_type: string, listener: EventListenerOrEventListenerObject) =>
            listeners.delete(listener as MediaChangeListener),
        addListener: (listener) => listeners.add(listener as MediaChangeListener),
        removeListener: (listener) => listeners.delete(listener as MediaChangeListener),
        dispatchEvent: () => true,
        setMatches(value: boolean) {
            matches = value;
            const event = { matches: value, media: this.media } as MediaQueryListEvent;
            listeners.forEach((listener) => listener(event));
        },
    };
}

beforeEach(() => {
    storage = createStorage();
    Reflect.deleteProperty(document, 'startViewTransition');
    document.documentElement.removeAttribute('data-theme');
    document.documentElement.removeAttribute('data-theme-appearance');
    document.documentElement.removeAttribute('style');
});

afterEach(() => {
    Reflect.deleteProperty(document, 'startViewTransition');
    vi.useRealTimers();
    vi.restoreAllMocks();
});

describe('theme controller', () => {
    it('applies the explicit dark default without exposing OS appearance state', () => {
        const controller = createThemeController({
            document,
            storage,
            reducedMotion: createMediaQuery(true),
        });

        controller.start();
        expect(document.documentElement.dataset.theme).toBe('tokyo-night');
        expect(document.documentElement.dataset.themeAppearance).toBe('dark');
        expect(get(controller.state)).not.toHaveProperty('systemAppearance');

        controller.destroy();
    });

    it('migrates a persisted System preference to explicit dark', () => {
        storage.setItem(THEME_STORAGE_KEY, JSON.stringify({
            mode: 'system',
            lightThemeId: 'catppuccin-latte',
            darkThemeId: 'nord',
        }));
        const controller = createThemeController({
            document,
            storage,
            reducedMotion: createMediaQuery(true),
        });

        controller.start();

        expect(get(controller.state).preference).toEqual({
            mode: 'dark',
            lightThemeId: 'catppuccin-latte',
            darkThemeId: 'nord',
        });
        expect(document.documentElement.dataset.theme).toBe('nord');
        expect(JSON.parse(storage.getItem(THEME_STORAGE_KEY) ?? '')).toEqual(
            get(controller.state).preference,
        );
        controller.destroy();
    });

    it('persists immutable preference updates and restores them on restart', () => {
        const environment = {
            document,
            storage,
            reducedMotion: createMediaQuery(true),
        };
        const first = createThemeController(environment);
        first.start();
        const before = get(first.state).preference;

        first.setPreferredTheme('dark', 'dracula');
        first.setMode('dark');

        const after = get(first.state).preference;
        expect(after).not.toBe(before);
        expect(before.darkThemeId).toBe('tokyo-night');
        expect(after).toEqual({
            mode: 'dark',
            lightThemeId: 'tdrive-light',
            darkThemeId: 'dracula',
        });
        expect(JSON.parse(storage.getItem(THEME_STORAGE_KEY) ?? '')).toEqual(after);
        first.destroy();

        const restored = createThemeController(environment);
        restored.start();
        expect(get(restored.state).preference).toEqual(after);
        expect(document.documentElement.dataset.theme).toBe('dracula');
        restored.destroy();
    });

    it('uses a view transition for a user-driven change when motion is allowed', () => {
        const finished = Promise.resolve();
        const startViewTransition = vi.fn((update: () => void) => {
            update();
            return { finished };
        });
        Object.defineProperty(document, 'startViewTransition', {
            configurable: true,
            value: startViewTransition,
        });
        const controller = createThemeController({
            document,
            storage,
            reducedMotion: createMediaQuery(false),
        });
        controller.start();

        controller.setPreferredTheme('dark', 'nord', { x: 120, y: 84 });

        expect(startViewTransition).toHaveBeenCalledOnce();
        expect(document.documentElement.dataset.theme).toBe('nord');
        expect(document.documentElement.style.getPropertyValue('--theme-origin-x')).toBe('120px');
        expect(document.documentElement.style.getPropertyValue('--theme-origin-y')).toBe('84px');
        controller.destroy();
    });

    it('consumes the expected ready rejection when a rapid change skips a transition', async () => {
        const ready = Promise.reject(new DOMException('Transition was skipped', 'AbortError'));
        const catchReady = vi.spyOn(ready, 'catch');
        Object.defineProperty(document, 'startViewTransition', {
            configurable: true,
            value: vi.fn((update: () => void) => {
                update();
                return { ready, finished: Promise.resolve() };
            }),
        });
        const controller = createThemeController({
            document,
            storage,
            reducedMotion: createMediaQuery(false),
        });
        controller.start();

        controller.setPreferredTheme('dark', 'nord');
        await Promise.resolve();

        expect(catchReady).toHaveBeenCalledOnce();
        controller.destroy();
    });

    it('changes instantly when the user requests reduced motion', () => {
        const startViewTransition = vi.fn((update: () => void) => {
            update();
            return { finished: Promise.resolve() };
        });
        Object.defineProperty(document, 'startViewTransition', {
            configurable: true,
            value: startViewTransition,
        });
        const controller = createThemeController({
            document,
            storage,
            reducedMotion: createMediaQuery(true),
        });
        controller.start();

        controller.setPreferredTheme('dark', 'catppuccin-mocha', { x: 10, y: 10 });

        expect(startViewTransition).not.toHaveBeenCalled();
        expect(document.documentElement.dataset.theme).toBe('catppuccin-mocha');
        controller.destroy();
    });

    it('uses and cleans up the CSS fallback on webviews without View Transitions', () => {
        vi.useFakeTimers();
        const controller = createThemeController({
            document,
            storage,
            reducedMotion: createMediaQuery(false),
        });
        controller.start();

        controller.setPreferredTheme('dark', 'dracula');

        expect(document.documentElement.dataset.theme).toBe('dracula');
        expect(document.documentElement.classList.contains('theme-transition-fallback')).toBe(true);
        expect(document.documentElement.style.getPropertyValue('--theme-origin-x')).toBe(`${window.innerWidth / 2}px`);

        vi.advanceTimersByTime(1249);
        expect(document.documentElement.classList.contains('theme-transition-fallback')).toBe(true);

        vi.advanceTimersByTime(1);
        expect(document.documentElement.classList.contains('theme-transition-fallback')).toBe(false);
        controller.destroy();
    });

    it('takes the CSS fallback instead of a view transition on Linux WebKit', () => {
        vi.useFakeTimers();
        const startViewTransition = vi.fn((update: () => void) => {
            update();
            return { finished: Promise.resolve() };
        });
        Object.defineProperty(document, 'startViewTransition', {
            configurable: true,
            value: startViewTransition,
        });
        const controller = createThemeController({
            document,
            storage,
            reducedMotion: createMediaQuery(false),
            userAgent: 'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15',
        });
        controller.start();

        controller.setPreferredTheme('dark', 'nord', { x: 120, y: 84 });

        expect(startViewTransition).not.toHaveBeenCalled();
        expect(document.documentElement.dataset.theme).toBe('nord');
        expect(document.documentElement.classList.contains('theme-transition-fallback')).toBe(true);
        expect(document.documentElement.style.getPropertyValue('--theme-origin-x')).toBe('120px');

        vi.advanceTimersByTime(1250);
        expect(document.documentElement.classList.contains('theme-transition-fallback')).toBe(false);
        controller.destroy();
    });

    it('keeps working when storage is unavailable and ignores invalid runtime input', () => {
        const unavailableStorage = {
            getItem: () => { throw new Error('blocked'); },
            setItem: () => { throw new Error('blocked'); },
        };
        const controller = createThemeController({
            document,
            storage: unavailableStorage,
            reducedMotion: createMediaQuery(true),
        });

        expect(() => controller.start()).not.toThrow();
        const initial = get(controller.state);
        (controller.setMode as (mode: string) => void)('sepia');
        (controller.setMode as (mode: string) => void)('system');
        controller.setPreferredTheme('light', 'dracula' as ThemeId);
        controller.setPreferredTheme('sepia' as ThemeAppearance, 'tdrive-light');
        expect(get(controller.state)).toBe(initial);

        expect(() => controller.setMode('light')).not.toThrow();
        expect(get(controller.state).preference.mode).toBe('light');
        controller.destroy();
    });

    it('composes rapid preference changes while a view transition is pending', () => {
        const pendingUpdates: Array<() => void> = [];
        Object.defineProperty(document, 'startViewTransition', {
            configurable: true,
            value: vi.fn((update: () => void) => {
                pendingUpdates.push(update);
                return { finished: new Promise<void>(() => {}) };
            }),
        });
        const controller = createThemeController({
            document,
            storage,
            reducedMotion: createMediaQuery(false),
        });
        controller.start();

        controller.setMode('light');
        controller.setPreferredTheme('light', 'catppuccin-latte');
        pendingUpdates[pendingUpdates.length - 1]?.();

        expect(get(controller.state).preference).toEqual({
            mode: 'light',
            lightThemeId: 'catppuccin-latte',
            darkThemeId: 'tokyo-night',
        });
        expect(document.documentElement.dataset.theme).toBe('catppuccin-latte');
        controller.destroy();
    });
});
