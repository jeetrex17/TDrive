// Reactive state that is local to the phone shell: which tab is showing, whether
// the drive switcher sheet is open, and the active drive's sync state for the
// header ring. Desktop never imports this, so the stores stay inert there.

import { derived, writable } from 'svelte/store';
import { sidebarState } from '../sidebar/sidebar-store';
import { fileListView } from '../file-list/file-list-store';
import { activeTransfers } from '../notifications/notif-store';
import type { DriveChannel } from '../../types';

export type MobileTab = 'files' | 'photos' | 'transfers' | 'account';

// Files is home (spec 2.4). Photos is a view of the same drive, so tapping it
// only toggles the gallery; the tab store still tracks which chrome to show.
export const activeTab = writable<MobileTab>('files');

export const driveSwitcherOpen = writable(false);

export type DriveSyncState = 'idle' | 'syncing' | 'synced' | 'failed';
// Fed by modules/channels.ts from the live_sync_* events and manual syncs.
export const driveSyncStatus = writable<DriveSyncState>('idle');

export function openDriveSwitcher(): void {
    driveSwitcherOpen.set(true);
}

export function closeDriveSwitcher(): void {
    driveSwitcherOpen.set(false);
}

// The drive currently shown, resolved from the sidebar's reactive snapshot so
// the header updates the moment a switch lands.
export const activeDrive = derived(sidebarState, ($sidebar): DriveChannel | null => {
    const id = $sidebar.activeChannelId;
    if (id == null) return null;
    return (
        $sidebar.personal.find((channel) => channel.id === id)
        ?? $sidebar.shared.find((channel) => channel.id === id)
        ?? null
    );
});

// Item count for the drive header. Rows only, and only when the list has
// loaded; state screens (loading/empty/error) report zero.
export const fileListCount = derived(fileListView, ($view) =>
    $view.kind === 'rows' ? $view.rows.length : 0,
);

// Badge on the Transfers tab: how many uploads or downloads are in flight.
export const activeTransferCount = derived(activeTransfers, ($transfers) => $transfers.length);

// Sandbox paths of finished single-file downloads, keyed by the transfer event
// id, so the Transfers tab can re-open the share sheet later. Populated by
// modules/transfers.ts on mobile only; folder downloads are not shareable yet.
export const downloadSharePaths = writable<Map<string, string>>(new Map());

export function rememberDownloadSharePath(transferId: string, path: string): void {
    if (!path) return;
    downloadSharePaths.update((paths) => {
        const next = new Map(paths);
        next.set(transferId, path);
        return next;
    });
}
