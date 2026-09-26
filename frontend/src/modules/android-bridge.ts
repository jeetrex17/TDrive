/**
 * Calling TDrive's own Android bridge and waiting for the answer.
 *
 * Wails' Go API has no room for the calls this app needs on Android -- picking
 * a document tree, moving a finished download into public storage -- so those
 * live on the host's JavaScript bridge instead and answer asynchronously. This
 * module is the plumbing they share: hand it a method name and its arguments,
 * get a promise.
 */

type CallbackTable = Record<string, (result: string | null, error: string | null) => void>;

interface BridgeWindow {
    wails?: Record<string, unknown>;
    _wailsAndroidCallback?: (id: string, result: string | null, error: string | null) => void;
    _tdriveBridgeCallbacks?: CallbackTable;
}

/** Whether the host exposes a given bridge method at all. */
export function hasBridgeMethod(name: string): boolean {
    if (typeof window === 'undefined') return false;
    return typeof (window as BridgeWindow).wails?.[name] === 'function';
}

/**
 * The bridge answers through one global the Wails runtime owns, so this chains
 * onto whatever is already installed rather than replacing it: taking that
 * global would silently break every other async call into the host.
 */
function registerCallback(id: string, resolve: (result: string) => void, reject: (error: Error) => void): void {
    const host = window as BridgeWindow;
    const table: CallbackTable = host._tdriveBridgeCallbacks ?? {};
    host._tdriveBridgeCallbacks = table;
    table[id] = (result, error) => {
        delete table[id];
        if (error) reject(new Error(error));
        else resolve(result ?? '');
    };

    if (!host._wailsAndroidCallback || !(host._wailsAndroidCallback as { tdriveChained?: boolean }).tdriveChained) {
        const previous = host._wailsAndroidCallback;
        const chained = (callbackId: string, result: string | null, error: string | null) => {
            const mine = host._tdriveBridgeCallbacks?.[callbackId];
            if (mine) {
                mine(result, error);
                return;
            }
            previous?.(callbackId, result, error);
        };
        (chained as { tdriveChained?: boolean }).tdriveChained = true;
        host._wailsAndroidCallback = chained;
    }
}

/** Calls a bridge method, rejecting with `unavailable` where the host has none. */
export function callBridge(name: string, args: string[], unavailable: string): Promise<string> {
    const host = window as BridgeWindow;
    const method = host.wails?.[name];
    if (typeof method !== 'function') return Promise.reject(new Error(unavailable));
    const id = `tdrive:${name}:${Date.now()}:${Math.random().toString(36).slice(2, 8)}`;
    return new Promise<string>((resolve, reject) => {
        registerCallback(id, resolve, reject);
        try {
            (method as (...callArgs: string[]) => void).call(host.wails, id, ...args);
        } catch (cause) {
            delete host._tdriveBridgeCallbacks?.[id];
            reject(cause instanceof Error ? cause : new Error(String(cause)));
        }
    });
}
