import { writable } from 'svelte/store';
import type { DriveChannel, PendingJoin } from '../../types';

export interface SidebarActionMenuRequest {
    x: number;
    y: number;
}

export interface SidebarState {
    personal: DriveChannel[];
    shared: DriveChannel[];
    pending: PendingJoin[];
    activeChannelId: number | null;
    /**
     * Which of the drive's virtual views is showing, mirrored from
     * state.virtualView rather than flattened to a boolean.
     *
     * It was `photosActive: boolean` until the trash became a destination of its
     * own, at which point `false` stopped meaning "the file list" and started
     * meaning "photos, or the trash, we cannot say". Everything that read it to
     * decide what to draw or where BACK goes was wrong in the trash -- most
     * visibly the phone, which exited the app instead of closing it.
     */
    virtualView: 'photos' | 'trash' | null;
}

const initialState: SidebarState = {
    personal: [],
    shared: [],
    pending: [],
    activeChannelId: null,
    virtualView: null,
};

export const sidebarState = writable<SidebarState>(initialState);

export function setSidebarState(next: SidebarState): void {
    sidebarState.set(next);
}

export function setSidebarVirtualView(virtualView: SidebarState['virtualView']): void {
    sidebarState.update((current) => (
        current.virtualView === virtualView ? current : { ...current, virtualView }
    ));
}
