import { Browser, Events, System, Window } from "@wailsio/runtime";
import {
    invokeRuntimeAsync,
    noopRuntimeUnsubscribe,
    RuntimeInvocationError,
    RuntimeUnavailableError,
    type RuntimeUnsubscribe,
} from "./gateway";

export { RuntimeInvocationError, RuntimeUnavailableError } from "./gateway";
export type { RuntimeUnsubscribe } from "./gateway";

/** The bits of Wails' System.Environment() result the frontend actually uses. */
export interface RuntimeEnvironment {
    buildType: string;
    platform: string;
    arch: string;
}

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

/**
 * Registers a typed Wails event listener and always returns a callable
 * teardown. The Go side always emits the event payload as a JSON array of the
 * original variadic args (including `[]` for no-arg events), so the wrapped
 * WailsEvent's `data` is spread back out to the typed callback.
 */
export function onRuntimeEvent<EventName extends RuntimeEventName>(
    eventName: EventName,
    callback: RuntimeEventCallback<EventName>,
): RuntimeUnsubscribe {
    let unsubscribe: RuntimeUnsubscribe | void;
    try {
        unsubscribe = Events.On(eventName, (event) => {
            const data = Array.isArray(event.data) ? event.data : (event.data == null ? [] : [event.data]);
            (callback as (...args: unknown[]) => void)(...(data as unknown[]));
        });
    } catch (cause) {
        throw new RuntimeInvocationError(`Events.On(${eventName})`, cause);
    }
    if (typeof unsubscribe !== "function") return noopRuntimeUnsubscribe;

    let active = true;
    return () => {
        if (!active) return;
        active = false;
        try {
            unsubscribe();
        } catch (cause) {
            throw new RuntimeInvocationError(`Events.On(${eventName}) teardown`, cause);
        }
    };
}

/**
 * Registers native file-drop handling. Wails v3 has no JS-level "enable file
 * drop" call: drops are delivered to Go as a native window event, and Go
 * re-emits it to the frontend as the ordinary `files_dropped` custom event.
 * This just gives that event a positional (x, y, paths) callback shape.
 */
export function onNativeFileDrop(callback: (x: number, y: number, paths: string[]) => void): RuntimeUnsubscribe {
    return onRuntimeEvent("files_dropped", (payload) => {
        const record = payload !== null && typeof payload === "object" && !Array.isArray(payload)
            ? payload as Record<string, unknown>
            : {};
        const paths = Array.isArray(record.paths)
            ? record.paths.filter((path): path is string => typeof path === "string")
            : [];
        const x = Number(record.x);
        const y = Number(record.y);
        callback(Number.isFinite(x) ? x : 0, Number.isFinite(y) ? y : 0, paths);
    });
}

/** Opens a URL natively when available and otherwise uses the browser fallback. */
export function openExternalUrl(url: string): void {
    if (isGatewayReady()) {
        void Browser.OpenURL(url).catch((cause) => {
            console.warn("Browser.OpenURL failed:", cause);
            openExternalUrlInBrowser(url);
        });
        return;
    }
    openExternalUrlInBrowser(url);
}

function openExternalUrlInBrowser(url: string): void {
    if (typeof window !== "undefined") window.open(url, "_blank", "noopener,noreferrer");
}

/**
 * Browser-preview override so the mobile branches can be exercised in Vite
 * without a device: `?mobile=1` previews a generic phone, `?mobile=ios` or
 * `?mobile=android` a specific one. A real webview loads the app without a
 * query string, so it never applies there.
 */
function mobileOverride(): "mobile" | "ios" | "android" | null {
    if (typeof window === "undefined") return null;
    const value = new URLSearchParams(window.location?.search ?? "").get("mobile");
    if (value === "ios" || value === "android") return value;
    return value === "1" ? "mobile" : null;
}

/** True on iOS and Android (real or previewed); false until the gateway is ready. */
export function isMobilePlatform(): boolean {
    return mobileOverride() !== null || (isGatewayReady() && System.IsMobile());
}

export function isIOSPlatform(): boolean {
    const override = mobileOverride();
    if (override) return override === "ios";
    return isGatewayReady() && System.IsIOS();
}

export function isAndroidPlatform(): boolean {
    const override = mobileOverride();
    if (override) return override === "android";
    return isGatewayReady() && System.IsAndroid();
}

/** Wails window fullscreen is a desktop feature; on a phone it is a no-op. */
export function fullscreenAvailable(): boolean {
    return isGatewayReady() && !isMobilePlatform();
}

export function enterFullscreen(): void {
    if (!isGatewayReady()) throw new RuntimeUnavailableError("Window.Fullscreen");
    void Window.Fullscreen().catch((cause) => console.warn("Window.Fullscreen failed:", cause));
}

export function exitFullscreen(): void {
    if (!isGatewayReady()) throw new RuntimeUnavailableError("Window.UnFullscreen");
    void Window.UnFullscreen().catch((cause) => console.warn("Window.UnFullscreen failed:", cause));
}

export async function isFullscreen(): Promise<boolean> {
    if (!isGatewayReady()) throw new RuntimeUnavailableError("Window.IsFullscreen");
    return Boolean(await invokeRuntimeAsync("Window.IsFullscreen", Window, Window.IsFullscreen));
}

// Wails v3 has no titlebar light/dark/system theme API (window.ts exposes no
// equivalent of v2's WindowSetLightTheme/WindowSetDarkTheme/
// WindowSetSystemDefaultTheme). These stay as no-ops so native-theme.ts's call
// sites keep working; only the background-colour sync below still applies.
export function setNativeSystemTheme(): void {}
export function setNativeLightTheme(): void {}
export function setNativeDarkTheme(): void {}

export function setNativeWindowBackgroundColour(red: number, green: number, blue: number, alpha: number): void {
    if (!isGatewayReady()) throw new RuntimeUnavailableError("Window.SetBackgroundColour");
    void Window.SetBackgroundColour(red, green, blue, alpha)
        .catch((cause) => console.warn("Window.SetBackgroundColour failed:", cause));
}

/** Resolves once a real Wails webview (as opposed to the browser preview) is ready. */
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
    return isGatewayReady();
}

/**
 * True inside a real Wails webview, false in the plain Vite dev/preview
 * browser. `@wailsio/runtime` always wires up `Events.On`/bound methods as
 * regular JS functions regardless of environment, so their mere presence
 * can't tell the two apart. `window._wails.environment` can: Go injects it
 * with an inline script before the app bundle loads, in every real webview
 * (dev or built), and nothing sets it in a plain browser tab.
 */
export function isGatewayReady(): boolean {
    if (typeof window === "undefined") return false;
    return Boolean(window._wails?.environment);
}

export async function getRuntimeEnvironment(): Promise<RuntimeEnvironment> {
    if (!isGatewayReady()) throw new RuntimeUnavailableError("System.Environment");
    const info = await invokeRuntimeAsync("System.Environment", System, System.Environment);
    return {
        buildType: info.Debug ? "dev" : "production",
        platform: info.OS,
        arch: info.Arch,
    };
}
