// Android hardware/predictive back and iOS's native leading-edge back gesture
// share one phone-shell contract.
//
// The host cannot answer the press on its own: WebView.canGoBack() only sees
// document navigations, so the history entries a single-page app pushes are
// invisible to it and every BACK press would leave the app. Instead the host
// asks the page through the bridge below and only exits when the press went
// unhandled (see build/android/app/src/main/java/com/wails/app/MainActivity).

import { get } from 'svelte/store';
import { breadcrumbPath } from '../chrome/breadcrumb-store';
import { sidebarState } from '../sidebar/sidebar-store';
import { closeTopSheet } from '../modals/sheet-stack';
import { selectionBarState } from '../selection/selection-bar-store';
import { navigateBack } from '../../modules/navigation';
import { exitPhotos } from '../../modules/gallery';
import { closeTrash } from '../../modules/trash/controller';
import { clearSelection } from '../../modules/selection';
import { activeTab, closeDriveSwitcher, driveSwitcherOpen } from './mobile-shell-store';

/** The name the native phone hosts call. Keep it in sync with MainActivity and main.m. */
export const BACK_BRIDGE = '__tdriveHandleBack';

/**
 * Dismisses the topmost surface, innermost first: whatever is drawn over the
 * page goes before the drive switcher, which goes before a selection, which
 * goes before the tab you are on, which goes before folder navigation.
 * Returns false at the root of the Files tab with nothing open, which is where
 * Android users expect BACK to leave the app.
 */
export function handleBackPress(): boolean {
    // Dialogs, sheets, the media players, the popover menus. They register
    // themselves, so a new one answers BACK without touching this file.
    if (closeTopSheet()) return true;
    if (get(driveSwitcherOpen)) {
        closeDriveSwitcher();
        return true;
    }
    // Selecting rows is a mode the tab bar disappears into, so leaving it is a
    // step back rather than a step out of the app.
    if (get(selectionBarState).active || get(selectionBarState).count > 0) {
        clearSelection();
        return true;
    }
    // Files is home. Every other destination steps back to it rather than out,
    // which is the one rule a phone's bottom bar is expected to keep.
    if (get(activeTab) === 'transfers' || get(activeTab) === 'account') {
        leaveVirtualView();
        activeTab.set('files');
        return true;
    }
    // A virtual view steps back to the file list. The trash has to be named
    // here as well as Photos: it has no breadcrumb to unwind, so without this
    // BACK fell through to `return false` and left the app.
    if (leaveVirtualView()) return true;
    if (get(breadcrumbPath).length > 0) {
        navigateBack();
        return true;
    }
    return false;
}

/** Closes whichever virtual view is showing, reporting whether one was. */
function leaveVirtualView(): boolean {
    const view = get(sidebarState).virtualView;
    if (view === 'photos') exitPhotos();
    else if (view === 'trash') closeTrash();
    else return false;
    return true;
}

/** Publishes the bridge for as long as the phone shell is mounted. */
export function activateMobileBack(): () => void {
    if (typeof window === 'undefined') return () => {};
    window[BACK_BRIDGE] = handleBackPress;
    return () => {
        delete window[BACK_BRIDGE];
    };
}
