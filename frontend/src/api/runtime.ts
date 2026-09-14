import type { WailsEventCallback, WailsRuntimeBridge, WailsRuntimeEnvironment } from "../global";
import {
    invokeRuntime,
    invokeRuntimeAsync,
    noopRuntimeUnsubscribe,
    RuntimeInvocationError,
    RuntimeUnavailableError,
    type RuntimeUnsubscribe,
} from "./gateway";

export { RuntimeInvocationError, RuntimeUnavailableError } from "./gateway";
export type { RuntimeUnsubscribe } from "./gateway";
export type RuntimeEnvironment = WailsRuntimeEnvironment;

/**
 * Every Wails event consumed by the frontend. Values remain unknown until the
 * receiving feature validates them, while event names and argument order stay
 * checked at the gateway boundary.
 */
export interface RuntimeEventMap {
    "login-success": [];
    "drive_scan_progress": [payload: unknown];
    "login-password-required": [];
    "login-error": [message: unknown];
    "login-code-invalid": [];
    gothint: [hint: unknown];
    "live_sync_started": [payload: unknown];
    "live_sync_completed": [payload: unknown];
    "live_sync_failed": [payload: unknown];
    download_progress: [percent: unknown];
    folder_download_progress: [payload: unknown];
    upload_start: [id: unknown, name: unknown, size: unknown, parentId: unknown];
    upload_progress: [id: unknown, percent: unknown];
    upload_complete: [id: unknown, name: unknown];
    upload_error: [id: unknown, name: unknown, message: unknown];
    import_start: [];
    import_progress: [payload: unknown];
    import_uploading: [payload: unknown];
    import_upload_progress: [payload: unknown];
    import_complete: [payload: unknown];
    files_dropped: [payload: unknown];
    preview_progress: [messageId: unknown, percent: unknown];
    native_media_state: [payload: unknown];
    encrypted_media_sessions_closed: [];
    "updates:open": [];
    update_state: [payload: unknown];
}

export type RuntimeEventName = keyof RuntimeEventMap;
export type RuntimeEventCallback<EventName extends RuntimeEventName> = (...data: RuntimeEventMap[EventName]) => void;

/** Registers a typed Wails event listener and always returns a callable teardown. */
export function onRuntimeEvent<EventName extends RuntimeEventName>(
    eventName: EventName,
    callback: RuntimeEventCallback<EventName>,
): RuntimeUnsubscribe {
    const runtime = nativeRuntime();
    const eventsOn = runtime?.EventsOn;
    if (!runtime || !eventsOn) return noopRuntimeUnsubscribe;

    let unsubscribe: RuntimeUnsubscribe | void;
    try {
        unsubscribe = eventsOn.call(runtime, eventName, callback as WailsEventCallback);
    } catch (cause) {
        throw new RuntimeInvocationError(`EventsOn(${eventName})`, cause);
    }
    if (typeof unsubscribe !== "function") return noopRuntimeUnsubscribe;

    let active = true;
    return () => {
        if (!active) return;
        active = false;
        try {
            unsubscribe();
        } catch (cause) {
            throw new RuntimeInvocationError(`EventsOn(${eventName}) teardown`, cause);
        }
    };
}

/**
 * Registers native file-drop handling when Wails exposes it. Browser previews
 * intentionally degrade to a no-op because no native drop transport exists.
 */
export function onNativeFileDrop(callback: (x: number, y: number, paths: string[]) => void): RuntimeUnsubscribe {
    const runtime = nativeRuntime();
    const onFileDrop = runtime?.OnFileDrop;
    if (!runtime || !onFileDrop) return noopRuntimeUnsubscribe;

    try {
        invokeRuntime("OnFileDrop", runtime, onFileDrop, callback, true);
    } catch {
        return noopRuntimeUnsubscribe;
    }

    const offFileDrop = runtime.OnFileDropOff;
    if (!offFileDrop) return noopRuntimeUnsubscribe;

    let active = true;
    return () => {
        if (!active) return;
        active = false;
        invokeRuntime("OnFileDropOff", runtime, offFileDrop);
    };
}

/** Opens a URL natively when available and otherwise uses the browser fallback. */
export function openExternalUrl(url: string): void {
    const runtime = nativeRuntime();
    const browserOpenUrl = runtime?.BrowserOpenURL;
    if (runtime && browserOpenUrl) {
        try {
            invokeRuntime("BrowserOpenURL", runtime, browserOpenUrl, url);
            return;
        } catch {
            // Browser fallback preserves the existing development-surface behavior.
        }
    }

    if (typeof window !== "undefined") {
        window.open(url, "_blank", "noopener,noreferrer");
    }
}

export function fullscreenAvailable(): boolean {
    const runtime = nativeRuntime();
    return Boolean(runtime?.WindowFullscreen && runtime.WindowUnfullscreen && runtime.WindowIsFullscreen);
}

export function enterFullscreen(): void {
    const runtime = nativeRuntime();
    const fullscreen = runtime?.WindowFullscreen;
    if (!runtime || !fullscreen) throw new RuntimeUnavailableError("WindowFullscreen");
    invokeRuntime("WindowFullscreen", runtime, fullscreen);
}

export function exitFullscreen(): void {
    const runtime = nativeRuntime();
    const unfullscreen = runtime?.WindowUnfullscreen;
    if (!runtime || !unfullscreen) throw new RuntimeUnavailableError("WindowUnfullscreen");
    invokeRuntime("WindowUnfullscreen", runtime, unfullscreen);
}

export async function isFullscreen(): Promise<boolean> {
    const runtime = nativeRuntime();
    const isNativeFullscreen = runtime?.WindowIsFullscreen;
    if (!runtime || !isNativeFullscreen) throw new RuntimeUnavailableError("WindowIsFullscreen");
    return Boolean(await invokeRuntimeAsync("WindowIsFullscreen", runtime, isNativeFullscreen));
}

export function setNativeSystemTheme(): void {
    const runtime = nativeRuntime();
    const setSystemTheme = runtime?.WindowSetSystemDefaultTheme;
    if (!runtime || !setSystemTheme) throw new RuntimeUnavailableError("WindowSetSystemDefaultTheme");
    invokeRuntime("WindowSetSystemDefaultTheme", runtime, setSystemTheme);
}

export function setNativeLightTheme(): void {
    const runtime = nativeRuntime();
    const setLightTheme = runtime?.WindowSetLightTheme;
    if (!runtime || !setLightTheme) throw new RuntimeUnavailableError("WindowSetLightTheme");
    invokeRuntime("WindowSetLightTheme", runtime, setLightTheme);
}

export function setNativeDarkTheme(): void {
    const runtime = nativeRuntime();
    const setDarkTheme = runtime?.WindowSetDarkTheme;
    if (!runtime || !setDarkTheme) throw new RuntimeUnavailableError("WindowSetDarkTheme");
    invokeRuntime("WindowSetDarkTheme", runtime, setDarkTheme);
}

export function setNativeWindowBackgroundColour(red: number, green: number, blue: number, alpha: number): void {
    const runtime = nativeRuntime();
    const setBackgroundColour = runtime?.WindowSetBackgroundColour;
    if (!runtime || !setBackgroundColour) throw new RuntimeUnavailableError("WindowSetBackgroundColour");
    invokeRuntime("WindowSetBackgroundColour", runtime, setBackgroundColour, red, green, blue, alpha);
}

/** Resolves once the late-injected Wails App and event runtime are both usable. */
export function waitForGatewayReady(timeoutMs = 4000): Promise<boolean> {
    if (isGatewayReady()) return Promise.resolve(true);
    if (typeof window === "undefined") return Promise.resolve(false);

    const deadline = Date.now() + Math.max(0, timeoutMs);
    const { promise, resolve } = Promise.withResolvers<boolean>();
    const tick = () => {
        if (isGatewayReady()) {
            resolve(true);
            return;
        }
        if (Date.now() >= deadline) {
            resolve(false);
            return;
        }
        window.setTimeout(tick, 30);
    };
    tick();
    return promise;
}

export function runtimeEventsAvailable(): boolean {
    return Boolean(nativeRuntime()?.EventsOn);
}

export function isGatewayReady(): boolean {
    if (typeof window === "undefined") return false;
    return Boolean(window.go?.main?.App && window.runtime?.EventsOn);
}

export async function getRuntimeEnvironment(): Promise<RuntimeEnvironment> {
    const runtime = nativeRuntime();
    const environment = runtime?.Environment;
    if (!runtime || !environment) throw new RuntimeUnavailableError("Environment");
    return await invokeRuntimeAsync("Environment", runtime, environment);
}

function nativeRuntime(): WailsRuntimeBridge | null {
    if (typeof window === "undefined") return null;
    return window.runtime ?? null;
}
