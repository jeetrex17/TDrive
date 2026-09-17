import { Browser, Events, System, Window } from "@wailsio/runtime";
import {
    Haptic as rawHaptic,
    SafeAreaInsets as rawSafeAreaInsets,
    SetImmersive as rawSetImmersive,
    SetKeyboardWatch as rawSetKeyboardWatch,
    SetScreenProtect as rawSetScreenProtect,
} from "../../bindings/TDrive/app";
import {
    invokeBackend,
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
    download_progress: [percent: unknown, requestId: unknown];
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
    native_media_state: [payload: unknown];
    encrypted_media_sessions_closed: [];
    "android:NetworkChanged": [payload: unknown];
    "android:BatteryChanged": [payload: unknown];
    "ios:NetworkChanged": [payload: unknown];
    "ios:BatteryChanged": [payload: unknown];
    gallery_memory_pressure: [];
    // Emitted by both phone hosts while SetKeyboardWatch is on; the payload is
    // {visible, height}. Android reports the only soft-keyboard height its
    // WebView knows, so this is the fallback where visualViewport is absent.
    "common:keyboard": [payload: unknown];
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

/**
 * The Android host answers `wails.platform()` synchronously from its
 * JavascriptInterface, which makes it usable before the environment has been
 * hydrated. The other hosts have no such call, so this only ever says
 * "android" or nothing.
 */
function bridgePlatform(): string | null {
    if (typeof window === "undefined") return null;
    const bridge = window.wails;
    // Android throws "Java bridge method can't be invoked on a non-injected
    // object" when an interface method is called without its receiver, so the
    // call has to stay attached to `window.wails`.
    if (!bridge || typeof bridge.platform !== "function") return null;
    try {
        return bridge.platform();
    } catch {
        return null;
    }
}

/** True on iOS and Android (real or previewed); false until the gateway is ready. */
export function isMobilePlatform(): boolean {
    return mobileOverride() !== null
        || (isGatewayReady() && (System.IsMobile() || bridgePlatform() === "android"));
}

export function isIOSPlatform(): boolean {
    const override = mobileOverride();
    if (override) return override === "ios";
    return isGatewayReady() && System.IsIOS();
}

export function isAndroidPlatform(): boolean {
    const override = mobileOverride();
    if (override) return override === "android";
    return isGatewayReady() && (System.IsAndroid() || bridgePlatform() === "android");
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
export async function waitForGatewayReady(timeoutMs = 4000): Promise<boolean> {
    if (typeof window === "undefined") return false;

    const deadline = Date.now() + Math.max(0, timeoutMs);
    while (!isGatewayReady()) {
        if (Date.now() >= deadline) return false;
        await new Promise((resolve) => window.setTimeout(resolve, 30));
    }
    await hydrateEnvironment();
    return true;
}

/**
 * Fills `window._wails.environment` on hosts that do not inject it (iOS and
 * Android in Wails 3 beta.22), so the runtime's `System.Is*` helpers and the
 * platform gating built on them answer correctly everywhere. A failure leaves
 * the platform helpers on their bridge fallbacks and is not fatal.
 */
async function hydrateEnvironment(): Promise<void> {
    if (window._wails?.environment) return;
    try {
        const info = await System.Environment();
        window._wails = window._wails ?? {};
        window._wails.environment = { OS: info.OS, Arch: info.Arch, Debug: info.Debug };
    } catch (cause) {
        console.warn("System.Environment failed:", cause);
    }
}

export function runtimeEventsAvailable(): boolean {
    return isGatewayReady();
}

/**
 * True inside a real Wails webview, false in the plain Vite dev/preview
 * browser. `@wailsio/runtime` always wires up `Events.On`/bound methods as
 * regular JS functions regardless of environment, so their mere presence
 * can't tell the two apart. `window._wails.environment` can on desktop, where
 * Go injects it with an inline script before the app bundle loads; the phone
 * hosts skip that script, so their message bridge stands in for it. Nothing
 * sets either in a plain browser tab.
 */
export function isGatewayReady(): boolean {
    if (typeof window === "undefined") return false;
    return Boolean(window._wails?.environment) || nativeBridgePresent();
}

/**
 * The message bridge each Wails host installs before the page runs: WebView2
 * on Windows, the WKWebView handler on macOS and iOS, the JavascriptInterface
 * on Android. It is the readiness signal on the phones, where beta.22 never
 * injects `_wails.environment`.
 */
function nativeBridgePresent(): boolean {
    return Boolean(
        window.chrome?.webview?.postMessage
        || window.webkit?.messageHandlers?.external?.postMessage
        || window.wails?.invoke,
    );
}

/** Screen edges the OS reserves for its own chrome. Zero off a phone. */
export interface SafeAreaInsets {
    top: number;
    bottom: number;
    left: number;
    right: number;
}

export async function getSafeAreaInsets(): Promise<SafeAreaInsets> {
    const raw = await invokeBackend(rawSafeAreaInsets);
    return {
        top: Number(raw?.top ?? 0),
        bottom: Number(raw?.bottom ?? 0),
        left: Number(raw?.left ?? 0),
        right: Number(raw?.right ?? 0),
    };
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

/**
 * The semantic feedback vocabulary both phone platforms implement. The names
 * describe the meaning, not the waveform: the OS picks the generator, and a
 * user who has turned system haptics off feels nothing.
 */
export type HapticKind =
    | "impact-light"
    | "impact-medium"
    | "impact-heavy"
    | "selection"
    | "success"
    | "warning"
    | "error";

/**
 * Plays one haptic. Fire-and-forget on purpose: feedback that arrives late is
 * worse than none, so nothing waits on it and a failure is swallowed. Silent
 * off a phone, where the backend call is a no-op stub.
 */
export function playHaptic(kind: HapticKind): void {
    if (!isGatewayReady()) return;
    void invokeBackend(rawHaptic, kind).catch(() => undefined);
}

/**
 * Asks the OS to keep app contents out of screenshots and the app switcher.
 *
 * Android honours this fully (FLAG_SECURE). iOS cannot block screenshots at
 * all, so there the same call only enables detection -- the switcher preview
 * still shows unless a native resign-active overlay is added to the host.
 */
export function setScreenProtect(enabled: boolean): void {
    if (!isGatewayReady()) return;
    void invokeBackend(rawSetScreenProtect, enabled).catch(() => undefined);
}

/** Starts or stops the host's "common:keyboard" {visible,height} events. */
export function setKeyboardWatch(enabled: boolean): void {
    if (!isGatewayReady()) return;
    void invokeBackend(rawSetKeyboardWatch, enabled).catch(() => undefined);
}

/**
 * Hides the phone's system bars for a full-screen surface, or gives them back.
 *
 * Only the video player asks. On Android 15 this is the only way to be rid of
 * the grey band the system paints down the edge for three-button navigation,
 * and on both platforms a picture with nothing over it is the point.
 */
export function setImmersive(enabled: boolean): void {
    if (!isGatewayReady() || !isMobilePlatform()) return;
    void invokeBackend(rawSetImmersive, enabled).catch(() => undefined);
}
