// Android back-button ownership for bottom sheets.
//
// The Android host routes a hardware BACK press to the WebView, and TDrive's
// SPA pushes no history of its own, so BACK with a sheet open would otherwise
// send the whole app to the launcher. Each mobile sheet pushes one history
// entry when it opens; a single popstate listener closes the topmost open
// sheet before the shell gets a chance to pop a folder level.
//
// The shell's own popstate handler must call closeTopSheetFromHistory() first
// and only navigate when it returns false, so an open sheet always wins BACK.

interface SheetEntry {
    id: number;
    close: () => void;
    fromHistory: boolean;
}

const stack: SheetEntry[] = [];
let nextId = 1;
let listening = false;
// Number of popstate events to swallow because we caused them with our own
// history.back() while balancing the stack.
let ignorePops = 0;

function onPopState(): void {
    if (ignorePops > 0) {
        ignorePops -= 1;
        return;
    }
    closeTopSheetFromHistory();
}

function ensureListener(): void {
    if (listening || typeof window === 'undefined') return;
    listening = true;
    window.addEventListener('popstate', onPopState);
}

export interface SheetHistoryHandle {
    /**
     * Call when the sheet closes through its own controls (Cancel, swipe,
     * scrim). Pops the history entry we added, but only while it is still the
     * top of the stack, so the back history never drifts.
     */
    release(): void;
}

/**
 * Registers an open sheet and pushes one history entry for it. `close` is the
 * sheet's own close path (it flips the owning store to closed).
 */
export function pushSheet(close: () => void): SheetHistoryHandle {
    ensureListener();
    const id = nextId++;
    const entry: SheetEntry = { id, close, fromHistory: false };
    stack.push(entry);
    try {
        window.history.pushState({ tdriveSheet: id }, '');
    } catch {
        // Some hosts (and happy-dom) restrict history; the sheet still works,
        // it just does not consume a BACK press.
    }
    return {
        release() {
            const index = stack.indexOf(entry);
            if (index >= 0) stack.splice(index, 1);
            // Only unwind history if our own entry is still on top; if BACK
            // already consumed it (fromHistory) or another entry sits above,
            // touching history here would close the wrong surface.
            if (entry.fromHistory) return;
            let onTop = false;
            try {
                onTop = (window.history.state as { tdriveSheet?: number } | null)?.tdriveSheet === id;
            } catch {
                onTop = false;
            }
            if (!onTop) return;
            ignorePops += 1;
            try {
                window.history.back();
            } catch {
                ignorePops -= 1;
            }
        },
    };
}

/**
 * Closes the topmost open sheet in response to a BACK press. Returns whether it
 * consumed the event so the shell knows to skip its own folder-level pop.
 */
export function closeTopSheetFromHistory(): boolean {
    const entry = stack[stack.length - 1];
    if (!entry) return false;
    stack.pop();
    // Mark so the sheet's release() does not call history.back() again: BACK
    // already removed our entry from the browser stack.
    entry.fromHistory = true;
    entry.close();
    return true;
}

/** True while at least one sheet owns a history entry. For the shell and tests. */
export function hasOpenSheet(): boolean {
    return stack.length > 0;
}
