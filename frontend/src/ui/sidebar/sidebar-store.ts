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
    photosActive: boolean;
}

const initialState: SidebarState = {
    personal: [],
    shared: [],
    pending: [],
    activeChannelId: null,
    photosActive: false,
};

export const sidebarState = writable<SidebarState>(initialState);

export function setSidebarState(next: SidebarState): void {
    sidebarState.set(next);
}

export function setSidebarPhotosActive(photosActive: boolean): void {
    sidebarState.update((current) => (
        current.photosActive === photosActive ? current : { ...current, photosActive }
    ));
}
