import { writable } from 'svelte/store';
import { notifPanelOpen, notifUnreadErrors } from './notif-store';

export type ActivityTab = 'transfers' | 'notifications';
export interface ActivityViewState {
    open: boolean;
    tab: ActivityTab;
    selectedId: string | null;
    restoreFocus: string;
}
export const activityView = writable<ActivityViewState>({ open: false, tab: 'transfers', selectedId: null, restoreFocus: '#notif-bell' });

/** Deliberate activation always opens, including after a hover preview. */
export function openActivity({ tab = 'transfers', selectedId = null, restoreFocus = '#notif-bell' }: Partial<Omit<ActivityViewState, 'open'>> = {}): void {
    notifPanelOpen.set(false);
    notifUnreadErrors.set(0);
    activityView.set({ open: true, tab, selectedId, restoreFocus });
}
export function closeActivity(): void {
    activityView.update((view) => ({ ...view, open: false }));
}
