// The bottom sheets that are open, oldest first.
//
// A sheet is the topmost surface on a phone, so Android's BACK press has to
// reach it before anything else. The host delivers that press to the shell
// (see ui/mobile/mobile-back), which asks this stack first. Nothing here
// touches session history: WebView.canGoBack() cannot see same-document
// entries, so a history-based scheme drops the press and exits the app.

interface OpenSheet {
    close: () => void;
}

const stack: OpenSheet[] = [];

export interface SheetHandle {
    /**
     * Call when the sheet closes through its own controls (Cancel, swipe,
     * scrim), so a later BACK press reaches the surface underneath.
     */
    release(): void;
}

/**
 * Registers an open sheet. `close` is the sheet's own close path: it flips the
 * owning store to closed, exactly as its Cancel button would.
 */
export function pushSheet(close: () => void): SheetHandle {
    const entry: OpenSheet = { close };
    stack.push(entry);
    return {
        release() {
            const index = stack.indexOf(entry);
            if (index >= 0) stack.splice(index, 1);
        },
    };
}

/**
 * Closes the topmost open sheet, reporting whether there was one so the caller
 * knows if the press was consumed. Stacked sheets close top first.
 */
export function closeTopSheet(): boolean {
    const entry = stack.pop();
    if (!entry) return false;
    entry.close();
    return true;
}

/** True while at least one sheet is open. */
export function hasOpenSheet(): boolean {
    return stack.length > 0;
}
