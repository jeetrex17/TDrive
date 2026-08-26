import type { Readable } from 'svelte/store';
import {
    Environment,
    WindowSetBackgroundColour,
    WindowSetDarkTheme,
    WindowSetLightTheme,
} from '../../../wailsjs/runtime/runtime';
import { themeState, type ThemeState } from './theme-controller';
import { getThemeDefinition } from './theme-model';

export interface NativeThemeRuntime {
    setBackgroundColour(red: number, green: number, blue: number, alpha: number): void;
    setLightTheme(): void;
    setDarkTheme(): void;
}

const wailsThemeRuntime: NativeThemeRuntime = {
    setBackgroundColour: WindowSetBackgroundColour,
    setLightTheme: WindowSetLightTheme,
    setDarkTheme: WindowSetDarkTheme,
};

/**
 * Mirrors web theme state into the native Wails window. The backdrop update is
 * cross-platform; titlebar theme calls are intentionally limited to Windows,
 * where Wails exposes explicit light/dark controls.
 */
export function connectNativeTheme(
    state: Readable<ThemeState>,
    platform: string,
    runtime: NativeThemeRuntime,
): () => void {
    return state.subscribe((themeState) => {
        const canvas = getThemeDefinition(themeState.resolvedThemeId).preview[0];
        const [red, green, blue] = parseHexColor(canvas);

        safely(() => runtime.setBackgroundColour(red, green, blue, 255));
        if (platform !== 'windows') return;

        if (themeState.preference.mode === 'light') {
            safely(runtime.setLightTheme);
        } else {
            safely(runtime.setDarkTheme);
        }
    });
}

/** Starts native synchronization after the Wails runtime has become ready. */
export async function initializeNativeTheme(): Promise<() => void> {
    try {
        const environment = await Environment();
        return connectNativeTheme(themeState, environment.platform, wailsThemeRuntime);
    } catch (error) {
        // Vite's browser preview has no Wails runtime; the web theme still works.
        console.warn('Native theme synchronization unavailable:', error);
        return () => {};
    }
}

function parseHexColor(hex: string): readonly [number, number, number] {
    const normalized = /^#[\da-f]{6}$/i.test(hex) ? hex.slice(1) : '1a1b26';
    return [
        Number.parseInt(normalized.slice(0, 2), 16),
        Number.parseInt(normalized.slice(2, 4), 16),
        Number.parseInt(normalized.slice(4, 6), 16),
    ];
}

function safely(action: () => void): void {
    try {
        action();
    } catch {
        // Native windows can disappear before frontend teardown completes.
    }
}
