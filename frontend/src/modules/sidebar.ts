// Sidebar — renders the list of drives (personal + shared) and routes
// clicks to channels.js. Re-renders on every loadChannels() call.

import { state } from '../state';
import {
    switchActiveChannel,
    getInviteLink,
    getApprovalInviteLink,
    checkPendingJoin,
    removePendingJoin,
} from './channels';
import { openShareDriveModal } from './modals/share-drive';
import { openLeaveDriveModal } from './modals/leave-drive';
import { openNewDriveModal } from './modals/new-drive';
import { openJoinDriveModal } from './modals/join-drive';
import { openJoinRequestsModal } from './modals/join-requests';
import { notify } from './notifications';
import { enterPhotos, exitPhotos } from './gallery';
import { showContextMenu, type ContextMenuItem } from './context-menu';
import DriveList from '../ui/sidebar/DriveList.svelte';
import {
    setSidebarState,
    type SidebarChannel,
    type SidebarPendingJoin,
} from '../ui/sidebar/sidebar-store';
import { mountSvelte, type SvelteMountHandle } from '../ui/mount';

let personalEl: HTMLElement | null = null;
let sharedEl: HTMLElement | null = null;
let personalDriveList: SvelteMountHandle<Record<string, unknown>> | null = null;
let sharedDriveList: SvelteMountHandle<Record<string, unknown>> | null = null;

export function setupSidebar() {
    personalEl = document.getElementById('drives-personal');
    sharedEl = document.getElementById('drives-shared');
    mountDriveLists();

    const newBtn = document.getElementById('open-new-drive');
    if (newBtn) newBtn.addEventListener('click', () => openNewDriveModal());
    const joinBtn = document.getElementById('open-join-drive');
    if (joinBtn) joinBtn.addEventListener('click', () => openJoinDriveModal());
    const photosBtn = document.getElementById('nav-photos');
    if (photosBtn) photosBtn.addEventListener('click', () => enterPhotos());

    renderSidebar();
}

export function renderSidebar() {
    if (!personalEl || !sharedEl) return;

    const channels = state.channels || [];
    const personal = channels.filter((c) => c?.kind === 'personal').map(normalizeChannel);
    const shared = channels.filter((c) => c?.kind === 'shared').map(normalizeChannel);
    const pending = Array.isArray(state.pendingJoins)
        ? state.pendingJoins.map(normalizePendingJoin).filter((p) => p.invite_hash)
        : [];

    setSidebarState({
        personal,
        shared,
        pending,
        activeChannelId: state.activeChannel ? Number(state.activeChannel.id) : null,
        photosActive: state.virtualView === 'photos',
    });

    // The Photos item owns the highlight while the gallery is open.
    document.getElementById('nav-photos')?.classList.toggle('active', state.virtualView === 'photos');
}

function mountDriveLists(): void {
    if (personalEl && !personalDriveList) {
        personalEl.replaceChildren();
        personalDriveList = mountSvelte(DriveList, {
            target: personalEl,
            props: {
                kind: 'personal' as const,
                onDriveClick: handleDriveClick,
            },
        });
    }

    if (sharedEl && !sharedDriveList) {
        sharedEl.replaceChildren();
        sharedDriveList = mountSvelte(DriveList, {
            target: sharedEl,
            props: {
                kind: 'shared' as const,
                onDriveClick: handleDriveClick,
                onDriveContextMenu: showSharedContextMenu,
                onPendingClick: handlePendingClick,
                onPendingContextMenu: showPendingContextMenu,
            },
        });
    }
}

function normalizeChannel(channel: any): SidebarChannel {
    return {
        id: Number(channel?.id || 0),
        title: String(channel?.title || ''),
        kind: String(channel?.kind || ''),
        is_active: Boolean(channel?.is_active),
    };
}

function normalizePendingJoin(pending: any): SidebarPendingJoin {
    return {
        invite_hash: String(pending?.invite_hash || ''),
        title: String(pending?.title || ''),
        last_error: String(pending?.last_error || ''),
    };
}

function handleDriveClick(channelId: number): void {
    if (Number(channelId) === Number(state.activeChannel?.id)) {
        // Clicking the already-active drive while in Photos returns to its files.
        if (state.virtualView === 'photos') exitPhotos();
        return;
    }
    void switchActiveChannel(Number(channelId));
}

async function handlePendingClick(inviteHash: string): Promise<void> {
    const pending = state.pendingJoins.find((item) => String(item?.invite_hash || '') === inviteHash);
    if (!pending) return;

    try {
        const result = await checkPendingJoin(inviteHash);
        if (result?.status === 'joined') {
            notify({
                level: 'success',
                title: 'Request approved',
                body: 'Joined the drive.',
            });
        } else {
            const lastError = String(result?.pending?.last_error || '').trim();
            notify({
                level: lastError ? 'error' : 'info',
                title: lastError ? 'Could not check request' : 'Still waiting for approval',
                body: lastError,
            });
        }
    } catch (err) {
        notify({
            level: 'error',
            title: 'Could not check request',
            body: String(err),
        });
    }
}

function showSharedContextMenu(event: MouseEvent, c: SidebarChannel) {
    const items: ContextMenuItem[] = [
        {
            label: 'Copy invite link',
            action: async () => {
                try {
                    const link = await getInviteLink(Number(c.id));
                    openShareDriveModal(link, { approvalRequired: false });
                } catch (err) {
                    notify({
                        level: 'error',
                        title: 'Could not get invite link',
                        body: String(err),
                    });
                }
            },
        },
        {
            label: 'Copy approval link',
            action: async () => {
                try {
                    const link = await getApprovalInviteLink(Number(c.id));
                    openShareDriveModal(link, { approvalRequired: true });
                } catch (err) {
                    notify({
                        level: 'error',
                        title: 'Could not get approval link',
                        body: String(err),
                    });
                }
            },
        },
        {
            label: 'Join requests',
            action: () => openJoinRequestsModal({ id: Number(c.id), title: c.title }),
        },
        {
            label: 'Leave drive',
            danger: true,
            action: () => openLeaveDriveModal({ id: Number(c.id), title: c.title }),
        },
    ];
    showContextMenu(event.clientX, event.clientY, items);
}

function showPendingContextMenu(event: MouseEvent, p: SidebarPendingJoin) {
    showContextMenu(event.clientX, event.clientY, [
        {
            label: 'Check now',
            action: async () => {
                try {
                    const result = await checkPendingJoin(String(p.invite_hash || ''));
                    const lastError = String(result?.pending?.last_error || '').trim();
                    notify({
                        level: result?.status === 'joined' ? 'success' : (lastError ? 'error' : 'info'),
                        title: result?.status === 'joined'
                            ? 'Request approved'
                            : (lastError ? 'Could not check request' : 'Still waiting for approval'),
                        body: result?.status === 'joined' ? 'Joined the drive.' : lastError,
                    });
                } catch (err) {
                    notify({
                        level: 'error',
                        title: 'Could not check request',
                        body: String(err),
                    });
                }
            },
        },
        {
            label: 'Remove request',
            danger: true,
            action: async () => {
                try {
                    await removePendingJoin(String(p.invite_hash || ''));
                    notify({
                        level: 'success',
                        title: 'Pending request removed',
                    });
                } catch (err) {
                    notify({
                        level: 'error',
                        title: 'Could not remove request',
                        body: String(err),
                    });
                }
            },
        },
    ]);
}
