// A phone OS ends a backgrounded app whenever it needs the memory (iOS does it
// routinely), so the tab and folder are remembered and put back on the next
// launch, the way a native app returns to where it was.

import { get } from 'svelte/store';
import { isMobilePlatform } from '../api';
import { state } from '../state';
import { breadcrumbPath } from '../ui/chrome/breadcrumb-store';
import { activeTab, type MobileTab } from '../ui/mobile/mobile-shell-store';
import { refreshFolderIndex } from './folder-index';
import { renderBreadcrumb } from './navigation';

export const PLACE_STORAGE_KEY = 'tdrive.place.v1';
const TABS: readonly MobileTab[] = ['files', 'photos', 'transfers', 'account'];

interface Place {
    tab: MobileTab;
    driveId: number;
    path: { id: string; name: string }[];
}

function readPlace(): Place | null {
    try {
        const value: unknown = JSON.parse(window.localStorage.getItem(PLACE_STORAGE_KEY) ?? 'null');
        if (!value || typeof value !== 'object') return null;
        const { tab, driveId, path } = value as Partial<Place>;
        if (!TABS.includes(tab as MobileTab) || !Number.isSafeInteger(driveId) || !Array.isArray(path)) return null;
        if (!path.every((entry) => typeof entry?.id === 'string' && typeof entry?.name === 'string')) return null;
        return { tab: tab as MobileTab, driveId: Number(driveId), path };
    } catch {
        return null;
    }
}

// Read once, before startup resets the path to the drive root and the
// remembering below records that reset over it.
let launchPlace: Place | null = typeof window === 'undefined' ? null : readPlace();

/** Records the tab and folder whenever either changes. */
export function rememberPlace(): () => void {
    if (!isMobilePlatform()) return () => {};
    const save = () => {
        const driveId = Number(state.activeChannel?.id ?? 0);
        if (!driveId) return;
        const place: Place = { tab: get(activeTab), driveId, path: get(breadcrumbPath) };
        try {
            window.localStorage.setItem(PLACE_STORAGE_KEY, JSON.stringify(place));
        } catch {
            // Without storage the app simply opens at the drive root.
        }
    };
    const stopTab = activeTab.subscribe(save);
    const stopPath = breadcrumbPath.subscribe(save);
    return () => {
        stopTab();
        stopPath();
    };
}

/**
 * Puts the shell back where the last launch left it, once per launch, if the
 * same drive is active and every folder on the way still exists.
 */
export async function restorePlace(): Promise<void> {
    const place = launchPlace;
    launchPlace = null;
    if (!place || !isMobilePlatform()) return;
    const driveId = Number(state.activeChannel?.id ?? 0);
    if (place.driveId !== driveId) return;
    if (place.path.length > 0) {
        let intact = false;
        try {
            const index = await refreshFolderIndex();
            intact = place.path.every((entry, depth) =>
                index.byId.get(entry.id)?.parentId === (depth === 0 ? '' : place.path[depth - 1].id));
        } catch {
            intact = false;
        }
        if (Number(state.activeChannel?.id ?? 0) !== driveId) return;
        if (intact) {
            state.folderPath = place.path.map(({ id, name }) => ({ id, name }));
            state.currentFolderId = place.path[place.path.length - 1].id;
            renderBreadcrumb();
        }
    }
    activeTab.set(place.tab);
}

/** Test seam: re-reads the stored place as a fresh launch would. */
export function reloadLaunchPlace(): void {
    launchPlace = readPlace();
}
