import { appActions } from './app-actions';
import { hasActiveModal } from '../ui/modals/modal-a11y';

type Refresh = () => void | Promise<void>;

let installed = false;

function isRefreshShortcut(event: KeyboardEvent): boolean {
    return (event.metaKey || event.ctrlKey)
        && !event.altKey
        && event.key.toLowerCase() === 'r';
}

/**
 * Cmd/Ctrl+R while a dialog is open consumes the press without refreshing: the
 * reload the WebView would otherwise do throws away whatever the dialog was
 * collecting, and re-listing the folder underneath a rename or a delete sheet
 * leaves the dialog talking about a row that is no longer in the list.
 *
 * "A dialog is open" is asked of the modal owner rather than of the document.
 * This used to scan for `.modal-overlay` elements whose inline display was not
 * `none`, which made an overlay's *style attribute* the record of whether it
 * was open -- so a dialog that closed by any other means, or one still mid
 * fade-out, answered wrong, and every press walked the whole document to find
 * out. The modal owner is the thing that already knows: everything that draws
 * over the page registers with it, from the desktop dialogs to the phone action
 * sheets to the video and preview players, and it answers in O(1).
 */
export function handleRefreshShortcut(
    event: KeyboardEvent,
    refresh: Refresh = () => appActions().triggerRefresh(),
): boolean {
    if (event.defaultPrevented || !isRefreshShortcut(event)) return false;

    event.preventDefault();
    if (event.repeat || hasActiveModal()) return true;

    void refresh();
    return true;
}

export function activateRefreshShortcut(): () => void {
    if (installed) return deactivateRefreshShortcut;
    installed = true;
    window.addEventListener('keydown', handleRefreshShortcut);
    return deactivateRefreshShortcut;
}

function deactivateRefreshShortcut(): void {
    if (!installed) return;
    installed = false;
    window.removeEventListener('keydown', handleRefreshShortcut);
}
