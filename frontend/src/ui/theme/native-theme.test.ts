import { readFileSync } from 'node:fs';
import { writable } from 'svelte/store';
import { describe, expect, it, vi } from 'vitest';
import type { ThemeState } from './theme-controller';
import { connectNativeTheme, type NativeThemeRuntime } from './native-theme';
import { getThemeDefinition, normalizeThemePreference, type ThemeMode } from './theme-model';

const nativeThemeSource = readFileSync(new URL('./native-theme.ts', import.meta.url), 'utf8');

function state(mode: ThemeMode, resolvedThemeId: ThemeState['resolvedThemeId']): ThemeState {
    const activeTheme = getThemeDefinition(resolvedThemeId);
    return {
        preference: normalizeThemePreference({
            mode,
            lightThemeId: 'tdrive-light',
            darkThemeId: resolvedThemeId === 'dracula' ? 'dracula' : 'tokyo-night',
        }),
        resolvedAppearance: activeTheme.appearance,
        resolvedThemeId,
    };
}

function runtime() {
    return {
        setBackgroundColour: vi.fn<NativeThemeRuntime['setBackgroundColour']>(),
        setLightTheme: vi.fn<NativeThemeRuntime['setLightTheme']>(),
        setDarkTheme: vi.fn<NativeThemeRuntime['setDarkTheme']>(),
    } satisfies NativeThemeRuntime;
}

describe('native theme bridge', () => {
    it('keeps the native window backdrop aligned with the active palette', () => {
        const theme = writable(state('light', 'tdrive-light'));
        const native = runtime();

        const disconnect = connectNativeTheme(theme, 'darwin', native);

        expect(native.setBackgroundColour).toHaveBeenLastCalledWith(243, 245, 249, 255);
        expect(native.setLightTheme).not.toHaveBeenCalled();
        disconnect();
    });

    it('synchronizes the Windows titlebar from explicit light and dark modes', () => {
        const theme = writable(state('light', 'tdrive-light'));
        const native = runtime();
        const disconnect = connectNativeTheme(theme, 'windows', native);

        expect(native.setLightTheme).toHaveBeenCalledOnce();

        theme.set(state('dark', 'dracula'));
        expect(native.setDarkTheme).toHaveBeenCalledOnce();
        expect(native.setBackgroundColour).toHaveBeenLastCalledWith(40, 42, 54, 255);
        disconnect();
    });

    it('selects the Windows light titlebar for a fixed light theme', () => {
        const theme = writable(state('light', 'tdrive-light'));
        const native = runtime();

        const disconnect = connectNativeTheme(theme, 'windows', native);

        expect(native.setLightTheme).toHaveBeenCalledOnce();
        disconnect();
    });

    it('does not retain the removed native System-theme runtime surface', () => {
        expect(nativeThemeSource).not.toContain('WindowSetSystemDefaultTheme');
        expect(nativeThemeSource).not.toContain('setSystemTheme');
    });

    it('isolates native-window teardown errors from frontend theme state', () => {
        const theme = writable(state('dark', 'tokyo-night'));
        const native: NativeThemeRuntime = {
            setBackgroundColour: () => { throw new Error('window closed'); },
            setLightTheme: () => { throw new Error('window closed'); },
            setDarkTheme: () => { throw new Error('window closed'); },
        };

        expect(() => connectNativeTheme(theme, 'windows', native)).not.toThrow();
    });

    it('stops native updates after disconnecting', () => {
        const theme = writable(state('dark', 'tokyo-night'));
        const native = runtime();
        const disconnect = connectNativeTheme(theme, 'windows', native);
        const callsBeforeDisconnect = native.setBackgroundColour.mock.calls.length;

        disconnect();
        theme.set(state('light', 'tdrive-light'));

        expect(native.setBackgroundColour).toHaveBeenCalledTimes(callsBeforeDisconnect);
    });
});
