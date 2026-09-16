// Reactive state that is local to the phone shell: which tab is showing, whether
// the drive switcher sheet is open, and the active drive's sync state for the
// header ring. Desktop never imports this, so the stores stay inert there.

import { derived, writable } from 'svelte/store';
import { sidebarState } from '../sidebar/sidebar-store';
import { fileListView } from '../file-list/file-list-store';
import { activeTransfers, historyEvents } from '../notifications/notif-store';
import type { DriveChannel } from '../../types';

export type MobileTab = 'files' | 'photos' | 'transfers' | 'account';

// Files is home (spec 2.4). Photos is a view of the same drive, so tapping it
// only toggles the gallery; the tab store still tracks which chrome to show.
export const activeTab = writable<MobileTab>('files');

export const driveSwitcherOpen = writable(false);

/**
 * True while a software keyboard is covering part of the screen.
 *
 * The tab bar steps aside for it rather than riding above it. Four
 * destinations are no use to someone halfway through typing a search, and a bar
 * parked on top of the keys is the shape every platform's own apps avoid.
 * See ui/mobile/keyboard-insets.ts, which sets this.
 */
export const keyboardOpen = writable(false);

export type DriveSyncState = 'idle' | 'syncing' | 'synced' | 'failed';

/**
 * What the one ambient mark in the header is saying. It answers a single
 * question -- is the app doing network work, and did any of it go wrong -- so
 * that the tab badge is free to answer a different one: is anything waiting for
 * me. Collapsing both into one mark is how a single cloud ends up standing for
 * six situations and meaning none of them.
 */
export type RingState = 'idle' | 'active' | 'attention' | 'failed';
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

// How many uploads or downloads are still on their way. Ambient, not a badge:
// the user started these and does not need to be told a number.
export const activeTransferCount = derived(activeTransfers, ($transfers) => $transfers.length);

/**
 * The Transfers badge: how many transfers need a person, not how many are
 * running. A badge is an interrupt -- it should mean "something is waiting for
 * you", and a count that lights up merely because an upload the user just
 * started is progressing is noise they learn to ignore, which costs the badge
 * its meaning for the one case that matters.
 *
 * Completed-awaiting-share belongs here too, but honestly cannot be counted
 * until the queue is durable: without persistence there is no way to record
 * that a finished download was already dealt with, so it would badge forever.
 */
export const transferAttentionCount = derived(historyEvents, ($events) =>
    $events.filter((event) => event.kind === 'transfer' && event.status === 'failed').length,
);

/**
 * Folds drive sync and transfer work into the single header mark. Failure
 * outranks progress: something that broke stays visible even while other work
 * carries on, because the broken thing is the part the user can act on.
 */
export const ringState = derived(
    [driveSyncStatus, activeTransfers, transferAttentionCount],
    ([$sync, $active, $attention]): RingState => {
        if ($sync === 'failed') return 'failed';
        if ($attention > 0) return 'attention';
        if ($sync === 'syncing' || $active.length > 0) return 'active';
        return 'idle';
    },
);

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
