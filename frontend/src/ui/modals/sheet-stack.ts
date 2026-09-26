// Everything currently drawn over the page, oldest first: dialogs, bottom
// sheets, the media players, the popover menus.
//
// These are the topmost surfaces on a phone, so Android's BACK press has to
// reach them before anything else. The host delivers that press to the shell
// (see ui/mobile/mobile-back), which asks this stack first. Nothing here
// touches session history: WebView.canGoBack() cannot see same-document
// entries, so a history-based scheme drops the press and exits the app.
//
// Most surfaces never register by hand: anything that installs a close path
// through ui/modals/modal-a11y is added and removed for it.

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
 *
 * The entry is dropped before `close` runs, so a surface that survives its own
 * close path -- one whose press only dismissed an inner layer -- has to push
 * itself back on. Dropping first is what stops a close path that silently does
 * nothing from swallowing every later press and trapping the user in the app.
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
