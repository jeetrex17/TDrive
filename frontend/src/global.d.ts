// Ambient declarations for the Wails v3 webview. Bound methods and runtime
// facilities are regular ES imports from "@wailsio/runtime" / the generated
// bindings now, so there is no injected `window.go` / `window.runtime` bridge
// to type here anymore.

export {};

declare global {
    interface PromiseConstructor {
        withResolvers<Value>(): {
            promise: Promise<Value>;
            resolve: (value: Value | PromiseLike<Value>) => void;
            reject: (reason?: unknown) => void;
        };
    }

    interface Window {
        /**
         * Populated by Go (see wails/v3 internal/runtime/runtime_{dev,prod}.go)
         * with an inline script that runs before the app bundle in the desktop
         * webviews. The iOS and Android hosts (beta.22) do not inject it, so
         * runtime.ts fills it from System.Environment() once the bridge is up.
         * Absent in the plain Vite dev/preview browser. The `@wailsio/runtime`
         * package declares a looser ambient `Window._wails` type of its own,
         * but it is not reachable from this project's tsconfig (nothing imports
         * it), so it is redeclared here.
         */
        _wails?: {
            environment?: {
                OS?: string;
                Arch?: string;
                Debug?: boolean;
            };
        };
        /** Android host bridge (addJavascriptInterface), present before the page runs. */
        wails?: {
            invoke?: (message: string) => void;
            platform?: () => string;
        };
        /** WKWebView message handler on macOS and iOS. */
        webkit?: {
            messageHandlers?: {
                external?: { postMessage?: (message: unknown) => void };
            };
        };
        /** WebView2 bridge on Windows. */
        chrome?: {
            webview?: { postMessage?: (message: unknown) => void };
        };
    }
}
