/**
 * Asking Android for a folder.
 *
 * Wails refuses directory selection on Android, and its reason is sound as far
 * as it goes: the Storage Access Framework returns document-tree URIs, and its
 * dialog API has nowhere to put one. So this does not go through that API. The
 * app's own Android bridge copies the chosen tree into the app cache and hands
 * back an ordinary directory path, which is the same answer Wails already uses
 * for single files, and which leaves every importer above this untouched.
 *
 * The copy is the cost: a folder is duplicated into cache before any of it is
 * uploaded. Reading straight from the tree URIs would avoid that, but it would
 * mean teaching the Go importer to read content URIs, which is a far larger
 * change for a phone-sized folder.
 */

interface AndroidBridge {
    pickFolder?(callbackId: string): void;
}

type CallbackTable = Record<string, (result: string | null, error: string | null) => void>;

interface BridgeWindow {
    wails?: AndroidBridge;
    _wailsAndroidCallback?: (id: string, result: string | null, error: string | null) => void;
    _tdriveFolderCallbacks?: CallbackTable;
}

/** Whether this build can ask for a folder at all. */
export function canPickFolder(): boolean {
    if (typeof window === 'undefined') return false;
    return typeof (window as BridgeWindow).wails?.pickFolder === 'function';
}

/**
 * The bridge answers through one global the Wails runtime owns, so this chains
 * onto whatever is already installed rather than replacing it: taking that
 * global would silently break every other async call into the host.
 */
function registerCallback(id: string, resolve: (path: string) => void, reject: (error: Error) => void): void {
    const host = window as BridgeWindow;
    const table: CallbackTable = host._tdriveFolderCallbacks ?? {};
    host._tdriveFolderCallbacks = table;
    table[id] = (result, error) => {
        delete table[id];
        if (error) reject(new Error(error));
        else resolve(result ?? '');
    };

    if (!host._wailsAndroidCallback || !(host._wailsAndroidCallback as { tdriveChained?: boolean }).tdriveChained) {
        const previous = host._wailsAndroidCallback;
        const chained = (callbackId: string, result: string | null, error: string | null) => {
            const mine = host._tdriveFolderCallbacks?.[callbackId];
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

/**
 * Opens the system folder picker and resolves with a local directory path, or
 * with an empty string if it was dismissed.
 */
export function pickAndroidFolder(): Promise<string> {
    const host = window as BridgeWindow;
    const pick = host.wails?.pickFolder;
    if (typeof pick !== 'function') {
        return Promise.reject(new Error('This build cannot open a folder picker.'));
    }
    const id = `tdrive-folder:${Date.now()}:${Math.random().toString(36).slice(2, 8)}`;
    return new Promise<string>((resolve, reject) => {
        registerCallback(id, resolve, reject);
        try {
            pick.call(host.wails, id);
        } catch (cause) {
            delete host._tdriveFolderCallbacks?.[id];
            reject(cause instanceof Error ? cause : new Error(String(cause)));
        }
    });
}
