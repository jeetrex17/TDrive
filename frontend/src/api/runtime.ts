import {
    BrowserOpenURL as rawBrowserOpenUrl,
    OnFileDrop as rawOnFileDrop,
    WindowFullscreen as rawWindowFullscreen,
    WindowIsFullscreen as rawWindowIsFullscreen,
    WindowSetDarkTheme as rawWindowSetDarkTheme,
    WindowSetLightTheme as rawWindowSetLightTheme,
    WindowSetSystemDefaultTheme as rawWindowSetSystemDefaultTheme,
    WindowUnfullscreen as rawWindowUnfullscreen,
} from "../../wailsjs/runtime/runtime";

export function onRuntimeEvent<TArgs extends unknown[]>(eventName: string, callback: (...data: TArgs) => void): (() => void) | null {
    const eventsOn = window.runtime?.EventsOn;
    if (!eventsOn) return null;
    const unsubscribe = eventsOn(eventName, (...data: unknown[]) => callback(...data as TArgs));
    return typeof unsubscribe === "function" ? unsubscribe : null;
}
export function onNativeFileDrop(callback: (x: number, y: number, paths: string[]) => void): void {
    try {
        rawOnFileDrop(callback, true);
    } catch {
        // The browser-only development surface has no native drop runtime.
    }
}
export function openExternalUrl(url: string): void {
    try {
        rawBrowserOpenUrl(url);
    } catch {
        window.open(url, "_blank", "noopener,noreferrer");
    }
}

export function fullscreenAvailable(): boolean {
    return Boolean(
        window.runtime?.WindowFullscreen
        && window.runtime?.WindowUnfullscreen
        && window.runtime?.WindowIsFullscreen
    );
}

export function enterFullscreen(): void {
    rawWindowFullscreen();
}

export function exitFullscreen(): void {
    rawWindowUnfullscreen();
}

export async function isFullscreen(): Promise<boolean> {
    return rawWindowIsFullscreen();
}

export function setNativeSystemTheme(): void {
    rawWindowSetSystemDefaultTheme();
}

export function setNativeLightTheme(): void {
    rawWindowSetLightTheme();
}

export function setNativeDarkTheme(): void {
    rawWindowSetDarkTheme();
}