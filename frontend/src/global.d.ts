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
         * with an inline script that runs before the app bundle, in every real
         * Wails webview. Absent in the plain Vite dev/preview browser, which is
         * how the gateway readiness check tells a native webview from a browser
         * preview. The `@wailsio/runtime` package declares a looser ambient
         * `Window._wails` type of its own, but it is not reachable from this
         * project's tsconfig (nothing imports it), so it is redeclared here.
         */
        _wails?: {
            environment?: {
                OS?: string;
                Arch?: string;
                Debug?: boolean;
            };
        };
    }
}
