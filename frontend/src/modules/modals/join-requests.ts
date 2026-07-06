// Admin modal for approval-required Telegram invite links.

import { approveJoinRequest, listJoinRequests, rejectJoinRequest } from '../channels';
import { notify } from '../notifications';
import JoinRequestsModal from '../../ui/modals/JoinRequestsModal.svelte';
import {
    joinRequestsList,
    joinRequestsModal,
    type JoinRequestRow,
} from '../../ui/modals/join-requests-modal-store';
import { mountSvelte, type SvelteMountHandle } from '../../ui/mount';

let joinRequestsModalHandle: SvelteMountHandle<Record<string, unknown>> | null = null;
let activeDriveId = 0;

export function setupJoinRequestsModal() {
    const modal = document.getElementById('join-requests-modal');
    if (!modal || joinRequestsModalHandle) return;

    modal.replaceChildren();
    joinRequestsModalHandle = mountSvelte(JoinRequestsModal, {
        target: modal,
        props: {
            onAction: resolveRequest,
        },
    });
}

export async function openJoinRequestsModal(drive: any) {
    const driveId = Number(drive?.id || 0);
    if (!driveId) return;

    activeDriveId = driveId;
    joinRequestsModal.open({ driveId, title: String(drive?.title || 'this drive') });
    await loadRequests();
}

function toRow(req: any): JoinRequestRow {
    const userId = Number(req?.user_id || 0);
    return {
        userId,
        displayName: String(req?.display_name || `User ${userId || ''}`).trim(),
        username: String(req?.username || ''),
        requestedAt: Number(req?.requested_at || 0),
    };
}

async function loadRequests(): Promise<void> {
    const driveId = activeDriveId;
    if (!driveId) return;

    joinRequestsList.set({ status: 'loading' });
    try {
        const rows = await listJoinRequests(driveId);
        if (driveId !== activeDriveId) return; // modal moved to another drive
        joinRequestsList.set({
            status: 'ready',
            rows: (Array.isArray(rows) ? rows : []).map(toRow),
            actingUserId: 0,
        });
    } catch (err) {
        if (driveId !== activeDriveId) return;
        joinRequestsList.set({ status: 'error', message: String(err) });
    }
}

async function resolveRequest(userId: number, approved: boolean): Promise<void> {
    const driveId = activeDriveId;
    if (!driveId || !userId) return;

    joinRequestsList.update((view) =>
        view.status === 'ready' ? { ...view, actingUserId: userId } : view,
    );
    try {
        if (approved) {
            await approveJoinRequest(driveId, userId);
        } else {
            await rejectJoinRequest(driveId, userId);
        }
        await loadRequests();
    } catch (err) {
        notify({
            level: 'error',
            title: `Could not ${approved ? 'approve' : 'reject'} request`,
            body: String(err),
        });
        joinRequestsList.update((view) =>
            view.status === 'ready' ? { ...view, actingUserId: 0 } : view,
        );
    }
}
