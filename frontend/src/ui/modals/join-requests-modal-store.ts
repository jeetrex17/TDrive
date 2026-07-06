import { writable } from 'svelte/store';
import { createModalController } from './modal-store';

export interface JoinRequestsPayload {
    driveId: number;
    title: string;
}

export interface JoinRequestRow {
    userId: number;
    displayName: string;
    username: string;
    requestedAt: number;
}

export type JoinRequestsListState =
    | { status: 'loading' }
    | { status: 'error'; message: string }
    | { status: 'ready'; rows: JoinRequestRow[]; actingUserId: number };

export const joinRequestsModal = createModalController<JoinRequestsPayload>();
export const joinRequestsList = writable<JoinRequestsListState>({ status: 'loading' });
