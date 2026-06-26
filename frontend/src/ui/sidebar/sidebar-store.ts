import { writable } from 'svelte/store';

export interface SidebarChannel {
    id: number;
    title: string;
    kind: 'personal' | 'shared' | string;
    is_active?: boolean;
}

export interface SidebarPendingJoin {
    invite_hash: string;
    title: string;
    last_error?: string;
}

export interface SidebarState {
    personal: SidebarChannel[];
    shared: SidebarChannel[];
    pending: SidebarPendingJoin[];
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
