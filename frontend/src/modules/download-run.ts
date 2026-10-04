/** One foreground download runs at a time, whether queued or resumed. */
let busy = false;
const idleListeners = new Set<() => void>();

export function downloadRunBusy(): boolean { return busy; }

export function tryBeginDownloadRun(): (() => void) | null {
    if (busy) return null;
    busy = true;
    let released = false;
    return () => {
        if (released) return;
        released = true;
        busy = false;
        for (const listener of idleListeners) listener();
    };
}

export function onDownloadRunIdle(listener: () => void): () => void {
    idleListeners.add(listener);
    return () => { idleListeners.delete(listener); };
}
