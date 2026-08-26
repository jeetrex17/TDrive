import { writable, type Readable } from 'svelte/store';
import {
    DEFAULT_THEME_PREFERENCE,
    isThemeAppearance,
    isThemeForAppearance,
    isThemeMode,
    normalizeThemePreference,
    resolveThemeId,
    type ThemeAppearance,
    type ThemeId,
    type ThemeMode,
    type ThemePreference,
} from './theme-model';

export const THEME_STORAGE_KEY = 'tdrive.appearance.v1';
export const THEME_TRANSITION_CLASS = 'theme-transition-active';
export const THEME_FALLBACK_CLASS = 'theme-transition-fallback';

const REDUCED_MOTION_QUERY = '(prefers-reduced-motion: reduce)';
const FALLBACK_CLEANUP_DELAY_MS = 1050;

type ThemeStorage = Pick<Storage, 'getItem' | 'setItem'>;

export interface ThemeChangeOrigin {
    readonly x: number;
    readonly y: number;
}

export interface ThemeState {
    readonly preference: ThemePreference;
    readonly resolvedAppearance: ThemeAppearance;
    readonly resolvedThemeId: ThemeId;
}

export interface ThemeControllerEnvironment {
    readonly document?: Document;
    readonly storage?: ThemeStorage;
    readonly reducedMotion?: MediaQueryList;
}

export interface ThemeController {
    readonly state: Readable<ThemeState>;
    start(): void;
    destroy(): void;
    setMode(mode: ThemeMode, origin?: ThemeChangeOrigin): void;
    setPreferredTheme(
        appearance: ThemeAppearance,
        themeId: ThemeId,
        origin?: ThemeChangeOrigin,
    ): void;
}

interface ResolvedEnvironment {
    readonly document?: Document;
    readonly storage?: ThemeStorage;
    readonly reducedMotion?: MediaQueryList;
}

export function createThemeController(
    environment: ThemeControllerEnvironment = {},
): ThemeController {
    let activeEnvironment: ResolvedEnvironment | undefined;
    let currentState = createThemeState(DEFAULT_THEME_PREFERENCE);
    // View Transition callbacks may run a frame after the user's click. Keep
    // the latest intended preference separately so a second quick selection
    // composes with, rather than overwrites, the first pending selection.
    let latestPreference = currentState.preference;
    let started = false;
    let fallbackTimer: ReturnType<typeof setTimeout> | undefined;
    let transitionGeneration = 0;
    const store = writable<ThemeState>(currentState);

    function applyState(nextState: ThemeState): void {
        currentState = nextState;
        latestPreference = nextState.preference;
        store.set(nextState);
        applyThemeAttributes(activeEnvironment?.document, nextState);
    }

    function stopTransition(): void {
        transitionGeneration += 1;
        if (fallbackTimer !== undefined) {
            clearTimeout(fallbackTimer);
            fallbackTimer = undefined;
        }
        removeTransitionClasses(activeEnvironment?.document);
    }

    function applyInstantly(nextState: ThemeState): void {
        stopTransition();
        applyState(nextState);
    }

    function applyWithFallback(nextState: ThemeState, origin?: ThemeChangeOrigin): void {
        const targetDocument = activeEnvironment?.document;
        const root = targetDocument?.documentElement;
        if (!root) {
            applyState(nextState);
            return;
        }

        const generation = beginTransition(targetDocument, THEME_FALLBACK_CLASS, origin);
        applyState(nextState);
        fallbackTimer = setTimeout(() => {
            if (generation !== transitionGeneration) return;
            root.classList.remove(THEME_FALLBACK_CLASS);
            fallbackTimer = undefined;
        }, FALLBACK_CLEANUP_DELAY_MS);
    }

    function applyWithViewTransition(nextState: ThemeState, origin?: ThemeChangeOrigin): boolean {
        const targetDocument = activeEnvironment?.document;
        const startViewTransition = targetDocument?.startViewTransition;
        if (!targetDocument || typeof startViewTransition !== 'function') return false;

        const generation = beginTransition(targetDocument, THEME_TRANSITION_CLASS, origin);
        let stateApplied = false;

        try {
            const transition = startViewTransition.call(targetDocument, () => {
                if (generation !== transitionGeneration || !started) return;
                stateApplied = true;
                applyState(nextState);
            });
            // Starting another transition automatically skips the previous
            // one in Chromium/WebView2. `ready` rejects in that normal case;
            // observe it so rapid palette scrubbing stays console-clean.
            if (transition?.ready) {
                void Promise.resolve(transition.ready).catch(() => undefined);
            }
            const cleanUp = () => {
                if (generation === transitionGeneration && !stateApplied && started) {
                    applyState(nextState);
                }
                finishTransition(targetDocument, generation);
            };
            if (transition?.finished) {
                // A rejected transition is a visual cancellation, not an app error.
                void Promise.resolve(transition.finished).then(cleanUp, cleanUp);
            } else {
                fallbackTimer = setTimeout(cleanUp, FALLBACK_CLEANUP_DELAY_MS);
            }
            return true;
        } catch {
            finishTransition(targetDocument, generation);
            if (!stateApplied) applyWithFallback(nextState, origin);
            return true;
        }
    }

    function transitionTo(nextState: ThemeState, origin?: ThemeChangeOrigin): void {
        const themeChanged = nextState.resolvedThemeId !== currentState.resolvedThemeId;
        if (!themeChanged || prefersReducedMotion(activeEnvironment?.reducedMotion)) {
            applyInstantly(nextState);
            return;
        }

        if (!applyWithViewTransition(nextState, origin)) {
            applyWithFallback(nextState, origin);
        }
    }

    function start(): void {
        if (started) return;

        const resolved = resolveEnvironment(environment);
        if (!hasRuntimeEnvironment(resolved)) return;

        activeEnvironment = resolved;
        started = true;
        const preference = readPreference(resolved.storage);
        latestPreference = preference;
        applyInstantly(createThemeState(preference));
    }

    function destroy(): void {
        stopTransition();
        activeEnvironment = undefined;
        started = false;
    }

    function updatePreference(preference: ThemePreference, origin?: ThemeChangeOrigin): void {
        if (preferencesEqual(preference, latestPreference)) return;

        latestPreference = preference;
        writePreference(activeEnvironment?.storage, preference);
        transitionTo(createThemeState(preference), origin);
    }

    function setMode(mode: ThemeMode, origin?: ThemeChangeOrigin): void {
        if (!isThemeMode(mode)) return;
        start();
        updatePreference(normalizeThemePreference({ ...latestPreference, mode }), origin);
    }

    function setPreferredTheme(
        appearance: ThemeAppearance,
        themeId: ThemeId,
        origin?: ThemeChangeOrigin,
    ): void {
        if (!isThemeAppearance(appearance) || !isThemeForAppearance(themeId, appearance)) return;
        start();
        const preference = appearance === 'light'
            ? { ...latestPreference, lightThemeId: themeId }
            : { ...latestPreference, darkThemeId: themeId };
        updatePreference(normalizeThemePreference(preference), origin);
    }

    return {
        state: { subscribe: store.subscribe },
        start,
        destroy,
        setMode,
        setPreferredTheme,
    };

    function beginTransition(
        targetDocument: Document,
        className: string,
        origin?: ThemeChangeOrigin,
    ): number {
        stopTransition();
        const generation = transitionGeneration;
        setTransitionOrigin(targetDocument, origin);
        targetDocument.documentElement.classList.add(className);
        return generation;
    }

    function finishTransition(targetDocument: Document, generation: number): void {
        if (generation !== transitionGeneration) return;
        if (fallbackTimer !== undefined) {
            clearTimeout(fallbackTimer);
            fallbackTimer = undefined;
        }
        removeTransitionClasses(targetDocument);
    }
}

function createThemeState(preference: ThemePreference): ThemeState {
    return Object.freeze({
        preference,
        resolvedAppearance: preference.mode,
        resolvedThemeId: resolveThemeId(preference),
    });
}

function resolveEnvironment(environment: ThemeControllerEnvironment): ResolvedEnvironment {
    const targetDocument = environment.document ?? getBrowserDocument();
    const targetWindow = targetDocument?.defaultView ?? getBrowserWindow();

    return {
        document: targetDocument,
        storage: environment.storage ?? getBrowserStorage(targetWindow),
        reducedMotion: environment.reducedMotion ?? queryMedia(targetWindow, REDUCED_MOTION_QUERY),
    };
}

function getBrowserDocument(): Document | undefined {
    return typeof document === 'undefined' ? undefined : document;
}

function getBrowserWindow(): Window | undefined {
    return typeof window === 'undefined' ? undefined : window;
}

function getBrowserStorage(targetWindow?: Window): ThemeStorage | undefined {
    try {
        return targetWindow?.localStorage;
    } catch {
        // Privacy modes and hardened webviews may deny storage access entirely.
        return undefined;
    }
}

function queryMedia(targetWindow: Window | undefined, query: string): MediaQueryList | undefined {
    try {
        return targetWindow?.matchMedia?.(query);
    } catch {
        return undefined;
    }
}

function hasRuntimeEnvironment(environment: ResolvedEnvironment): boolean {
    return Boolean(environment.document || environment.storage || environment.reducedMotion);
}

function readPreference(storage?: ThemeStorage): ThemePreference {
    let serialized: string | null | undefined;
    try {
        serialized = storage?.getItem(THEME_STORAGE_KEY);
    } catch {
        return normalizeThemePreference(null);
    }

    if (!serialized) return normalizeThemePreference(null);

    try {
        const preference = normalizeThemePreference(JSON.parse(serialized) as unknown);
        if (serialized !== JSON.stringify(preference)) writePreference(storage, preference);
        return preference;
    } catch {
        return normalizeThemePreference(null);
    }
}

function writePreference(storage: ThemeStorage | undefined, preference: ThemePreference): void {
    try {
        storage?.setItem(THEME_STORAGE_KEY, JSON.stringify(preference));
    } catch {
        // Appearance remains fully functional when persistence is unavailable.
    }
}

function prefersReducedMotion(reducedMotion?: MediaQueryList): boolean {
    try {
        return reducedMotion?.matches === true;
    } catch {
        return true;
    }
}

function applyThemeAttributes(targetDocument: Document | undefined, state: ThemeState): void {
    const root = targetDocument?.documentElement;
    if (!root) return;

    root.dataset.theme = state.resolvedThemeId;
    root.dataset.themeAppearance = state.resolvedAppearance;
    root.style.colorScheme = state.resolvedAppearance;
}

function setTransitionOrigin(targetDocument: Document, origin?: ThemeChangeOrigin): void {
    const root = targetDocument.documentElement;
    const viewportWidth = targetDocument.defaultView?.innerWidth || root.clientWidth;
    const viewportHeight = targetDocument.defaultView?.innerHeight || root.clientHeight;
    const x = normalizedCoordinate(origin?.x, viewportWidth);
    const y = normalizedCoordinate(origin?.y, viewportHeight);

    root.style.setProperty('--theme-origin-x', `${x}px`);
    root.style.setProperty('--theme-origin-y', `${y}px`);
}

function normalizedCoordinate(value: number | undefined, viewportSize: number): number {
    const fallback = Math.max(0, viewportSize / 2);
    if (typeof value !== 'number' || !Number.isFinite(value)) return fallback;
    if (viewportSize <= 0) return Math.max(0, value);
    return Math.min(Math.max(0, value), viewportSize);
}

function removeTransitionClasses(targetDocument?: Document): void {
    targetDocument?.documentElement.classList.remove(THEME_TRANSITION_CLASS, THEME_FALLBACK_CLASS);
}

function preferencesEqual(left: ThemePreference, right: ThemePreference): boolean {
    return left.mode === right.mode
        && left.lightThemeId === right.lightThemeId
        && left.darkThemeId === right.darkThemeId;
}

export const themeController = createThemeController();
export const themeState = themeController.state;

/** Initializes the singleton and returns an optional lifecycle cleanup callback. */
export function initializeTheme(): () => void {
    themeController.start();
    return () => themeController.destroy();
}

export function setThemeMode(mode: ThemeMode, origin?: ThemeChangeOrigin): void {
    themeController.setMode(mode, origin);
}

export function setPreferredTheme(
    appearance: ThemeAppearance,
    themeId: ThemeId,
    origin?: ThemeChangeOrigin,
): void {
    themeController.setPreferredTheme(appearance, themeId, origin);
}
