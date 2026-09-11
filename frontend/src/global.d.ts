// Ambient declarations for the Wails bridge and temporary app-level callbacks.

export {};

type WailsEventCallback = (...data: unknown[]) => void;

interface WailsRuntimeBridge {
    EventsOn?: (eventName: string, callback: WailsEventCallback) => (() => void) | void;
    OnFileDrop?: (callback: (x: number, y: number, paths: string[]) => void, useDropTarget: boolean) => void;
    BrowserOpenURL?: (url: string) => void;
    WindowFullscreen?: () => void;
    WindowUnfullscreen?: () => void;
    WindowIsFullscreen?: () => Promise<boolean>;
}

declare global {
    interface Window {
        go: { main: { App: Record<string, (...args: unknown[]) => unknown> } };
        runtime: WailsRuntimeBridge;

    }
}
