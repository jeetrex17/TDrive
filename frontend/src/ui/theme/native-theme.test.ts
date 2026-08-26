import { writable } from 'svelte/store';
import { describe, expect, it, vi } from 'vitest';
import type { ThemeState } from './theme-controller';
import { connectNativeTheme, type NativeThemeRuntime } from './native-theme';
import { getThemeDefinition, normalizeThemePreference } from './theme-model';

function state(mode: 'system' | 'light' | 'dark', resolvedThemeId: ThemeState['resolvedThemeId']): ThemeState {
    const activeTheme = getThemeDefinition(resolvedThemeId);
    return {
        preference: normalizeThemePreference({
            mode,
            lightThemeId: 'tdrive-light',
            darkThemeId: resolvedThemeId === 'dracula' ? 'dracula' : 'tokyo-night',
        }),
        systemAppearance: activeTheme.appearance,
        resolvedAppearance: activeTheme.appearance,
        resolvedThemeId,
    };
}

function runtime() {
    return {
        setBackgroundColour: vi.fn<NativeThemeRuntime['setBackgroundColour']>(),
        setSystemTheme: vi.fn<NativeThemeRuntime['setSystemTheme']>(),
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

    it('synchronizes the Windows titlebar without overriding System mode', () => {
        const theme = writable(state('system', 'tdrive-light'));
        const native = runtime();
        const disconnect = connectNativeTheme(theme, 'windows', native);

        expect(native.setSystemTheme).toHaveBeenCalledOnce();

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
        expect(native.setSystemTheme).not.toHaveBeenCalled();
        disconnect();
    });

    it('isolates native-window teardown errors from frontend theme state', () => {
        const theme = writable(state('dark', 'tokyo-night'));
        const native: NativeThemeRuntime = {
            setBackgroundColour: () => { throw new Error('window closed'); },
            setSystemTheme: () => { throw new Error('window closed'); },
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
