// Ambient declarations for the Wails bridge. The webview injects these at runtime,
// so gateway code must still feature-detect every capability before invoking it.

import type * as GeneratedAppBindings from "../wailsjs/go/main/App";

export {};

type WailsBinding<Binding> = Binding extends (...args: infer Args) => Promise<infer Result>
    ? (...args: Args) => Result | Promise<Result>
    : never;

/** Bound Go methods can be synchronous in test/dev bridges or promise-based in Wails. */
export type WailsAppBridge = {
    [Method in keyof typeof GeneratedAppBindings]: WailsBinding<typeof GeneratedAppBindings[Method]>;
};

export type WailsEventCallback = (...data: unknown[]) => void;
export type WailsEventUnsubscribe = () => void;

export interface WailsRuntimeEnvironment {
    buildType: string;
    platform: string;
    arch: string;
}

export interface WailsRuntimeBridge {
    EventsOn?: (eventName: string, callback: WailsEventCallback) => WailsEventUnsubscribe | void;
    OnFileDrop?: (callback: (x: number, y: number, paths: string[]) => void, useDropTarget: boolean) => void;
    OnFileDropOff?: () => void;
    BrowserOpenURL?: (url: string) => void;
    Environment?: () => WailsRuntimeEnvironment | Promise<WailsRuntimeEnvironment>;
    WindowFullscreen?: () => void;
    WindowUnfullscreen?: () => void;
    WindowIsFullscreen?: () => boolean | Promise<boolean>;
    WindowSetBackgroundColour?: (red: number, green: number, blue: number, alpha: number) => void;
    WindowSetDarkTheme?: () => void;
    WindowSetLightTheme?: () => void;
    WindowSetSystemDefaultTheme?: () => void;
}

declare global {
    interface PromiseConstructor {
        withResolvers<Value>(): {
            promise: Promise<Value>;
            resolve: (value: Value | PromiseLike<Value>) => void;
            reject: (reason?: unknown) => void;
        };
    }

    interface Window {
        go: { main: { App: WailsAppBridge } };
        runtime: WailsRuntimeBridge;
    }
}
