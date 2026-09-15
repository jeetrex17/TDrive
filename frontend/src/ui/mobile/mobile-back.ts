// Android hardware BACK support for the phone shell.
//
// The Android host already routes BACK as `if (webView.canGoBack()) goBack()
// else exit`, so the SPA has to own a history entry for every level the user can
// back out of, otherwise BACK jumps straight to the launcher. We keep exactly
// one entry per open folder level plus one while Photos is open plus one while
// the drive switcher sheet is open, and dismiss the topmost of those on
// `popstate`. At the drive root with nothing open we own no entry, so BACK falls
// through to the host and exits, which is what Android users expect.
//
// ModalShell sheets (owned by a separate surface, see ui/modals/sheet-history)
// push their own entry and close themselves on BACK. Which entry a BACK removed
// is read from `history.state` rather than from listener order: a sheet's entry
// leaves our depth intact, a folder entry lowers it.

import { get } from 'svelte/store';
import { breadcrumbPath } from '../chrome/breadcrumb-store';
import { sidebarState } from '../sidebar/sidebar-store';
import { hasActiveModal } from '../modals/modal-a11y';
import { navigateBack } from '../../modules/navigation';
import { exitPhotos } from '../../modules/gallery';
import { driveSwitcherOpen, closeDriveSwitcher } from './mobile-shell-store';

function desiredDepth(): number {
    const folders = get(breadcrumbPath).length;
    const photos = get(sidebarState).photosActive ? 1 : 0;
    const switcher = get(driveSwitcherOpen) ? 1 : 0;
    return folders + photos + switcher;
}

export function activateMobileBack(): () => void {
    if (typeof window === 'undefined') return () => {};

    // Entries we have pushed and still own. Only reconcile() grows it; only a
    // real BACK (popstate) or a programmatic reset (history.go below) shrinks it.
    let owned = 0;
    // popstate events we triggered ourselves (via history.go) and must ignore.
    let suppress = 0;

    function reconcile(): void {
        const want = desiredDepth();
        if (want > owned) {
            while (owned < want) {
                owned += 1;
                history.pushState({ tdriveBack: owned }, '');
            }
            return;
        }
        if (want < owned) {
            // The app navigated up on its own (in-app back, drive switch that
            // resets the folder path). Drop the now-stale entries so a later
            // BACK is never a dead no-op before the app exits.
            const drop = owned - want;
            owned = want;
            suppress += drop;
            history.go(-drop);
        }
    }

    function onPopState(): void {
        if (suppress > 0) {
            suppress -= 1;
            return;
        }
        // The entry BACK removed was a sheet's (its own listener closes it):
        // our depth is untouched, so there is nothing for us to dismiss.
        const depth = (history.state as { tdriveBack?: number } | null)?.tdriveBack ?? 0;
        if (depth >= owned) return;
        // A fullscreen viewer without a history entry of its own stays open;
        // keep our count honest so a later BACK does not exit one level early.
        if (hasActiveModal()) {
            owned = depth;
            return;
        }

        if (get(driveSwitcherOpen)) {
            owned = Math.max(0, owned - 1);
            closeDriveSwitcher();
            return;
        }
        if (get(sidebarState).photosActive) {
            owned = Math.max(0, owned - 1);
            exitPhotos();
            return;
        }
        if (get(breadcrumbPath).length > 0) {
            owned = Math.max(0, owned - 1);
            navigateBack();
            return;
        }
        // Nothing left to dismiss; owned is already 0, so the app exits.
    }

    const unsubscribers = [
        breadcrumbPath.subscribe(reconcile),
        sidebarState.subscribe(reconcile),
        driveSwitcherOpen.subscribe(reconcile),
    ];
    window.addEventListener('popstate', onPopState);

    return () => {
        for (const unsubscribe of unsubscribers) unsubscribe();
        window.removeEventListener('popstate', onPopState);
    };
}
