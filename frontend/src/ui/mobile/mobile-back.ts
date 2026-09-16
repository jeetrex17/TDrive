// Android hardware and gesture BACK for the phone shell.
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
import { navigateBack } from '../../modules/navigation';
import { exitPhotos } from '../../modules/gallery';
import { closeDriveSwitcher, driveSwitcherOpen } from './mobile-shell-store';

/** The name the Android host calls. Changing it means changing MainActivity. */
export const BACK_BRIDGE = '__tdriveHandleBack';

/**
 * Dismisses the topmost surface, innermost first: sheets sit above the drive
 * switcher, which sits above the gallery, which sits above folder navigation.
 * Returns false at the drive root with nothing open, which is where Android
 * users expect BACK to leave the app.
 */
export function handleBackPress(): boolean {
    if (closeTopSheet()) return true;
    if (get(driveSwitcherOpen)) {
        closeDriveSwitcher();
        return true;
    }
    if (get(sidebarState).photosActive) {
        exitPhotos();
        return true;
    }
    if (get(breadcrumbPath).length > 0) {
        navigateBack();
        return true;
    }
    return false;
}

/** Publishes the bridge for as long as the phone shell is mounted. */
export function activateMobileBack(): () => void {
    if (typeof window === 'undefined') return () => {};
    window[BACK_BRIDGE] = handleBackPress;
    return () => {
        delete window[BACK_BRIDGE];
    };
}
