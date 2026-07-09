// Drive (channel) management — Step 4 shared drives.
//
// Wraps the Wails methods exposed by app_channels.go and the existing
// SetActiveChannel / SyncChannel methods. Keeps state.channels and
// state.activeChannel in sync; tells the sidebar to re-render on every
// change.

import { state, resetFolderCaches, resetSelection } from '../state';
import {
    ListChannels,
    CreateSharedDrive,
    JoinSharedDrive,
    GetInviteLink,
    GetApprovalInviteLink,
    LeaveSharedDrive,
    ListPendingJoins,
    CheckPendingJoin,
    RemovePendingJoin,
    ListJoinRequests,
    ApproveJoinRequest,
    RejectJoinRequest,
    SetActiveChannel,
    SyncChannel,
} from '../../wailsjs/go/main/App';
import { runGlobalSearch } from './search';

let renderSidebar = () => {};
let refreshFilesView = () => {};
const pendingLiveSyncChannels = new Set<number>();
let processingLiveSyncRefresh = false;
let liveSyncEventsBound = false;

export function bindChannelsRenderers({ onSidebarUpdate, onActiveDriveChanged }: { onSidebarUpdate: any; onActiveDriveChanged: any }) {
    if (typeof onSidebarUpdate === 'function') renderSidebar = onSidebarUpdate;
    if (typeof onActiveDriveChanged === 'function') refreshFilesView = onActiveDriveChanged;
}

function applyChannels(list: any) {
    const arr = Array.isArray(list) ? list : [];
    state.channels = arr;
    const active = arr.find((c: any) => c && c.is_active) || arr.find((c: any) => c && c.kind === 'personal') || null;
    state.activeChannel = active
        ? { id: Number(active.id), title: String(active.title || ''), kind: String(active.kind || '') }
        : null;
    applyDriveKindUI();
}

function applyPendingJoins(list: any) {
    state.pendingJoins = Array.isArray(list) ? list : [];
}

// Hook for per-drive-kind UI tweaks. Step 4 hid folder controls in shared
// drives; Step 5 unlocks shared folders so the function is currently a
// no-op. Kept as a hook because Step 6 polish (uploader chips, member
// count, unread dots) will likely want to toggle UI based on drive kind.
function applyDriveKindUI() {
    // Intentionally empty.
}

export async function loadChannels() {
    try {
        const [channels, pending] = await Promise.all([
            ListChannels(),
            ListPendingJoins().catch((err) => {
                console.warn('ListPendingJoins failed:', err);
                return [];
            }),
        ]);
        applyChannels(channels);
        applyPendingJoins(pending);
        renderSidebar();
    } catch (err) {
        console.error('ListChannels failed:', err);
        state.channels = [];
        state.activeChannel = null;
        state.pendingJoins = [];
        renderSidebar();
    }
}

export function setupLiveSyncEvents() {
    if (liveSyncEventsBound) return;
    if (!window.runtime?.EventsOn) return;
    liveSyncEventsBound = true;

    window.runtime.EventsOn("live_sync_completed", (payload: any) => {
        queueLiveSyncRefresh(payload);
    });
    window.runtime.EventsOn("live_sync_failed", (payload: any) => {
        const channelID = Number(payload?.channel_id || 0);
        const activeID = Number(state.activeChannel?.id || 0);
        if (channelID && channelID !== activeID) return;
        console.warn("live sync failed:", payload?.error || payload);
    });
}

function queueLiveSyncRefresh(payload: any) {
    const channelID = Number(payload?.channel_id || 0);
    if (!channelID) return;
    pendingLiveSyncChannels.add(channelID);
    if (processingLiveSyncRefresh) return;
    processingLiveSyncRefresh = true;
    void processLiveSyncRefreshes();
}

async function processLiveSyncRefreshes() {
    try {
        while (pendingLiveSyncChannels.size > 0) {
            const changedChannels = new Set(pendingLiveSyncChannels);
            pendingLiveSyncChannels.clear();

            await loadChannels();
            const activeID = Number(state.activeChannel?.id || 0);
            if (!activeID || !changedChannels.has(activeID)) continue;

            state.telegramRootCache = null;
            if (String(state.searchQuery || "").trim()) {
                runGlobalSearch();
            } else {
                await refreshFilesView();
            }
        }
    } finally {
        processingLiveSyncRefresh = false;
    }
}

export async function createSharedDrive(title: any, requireApproval = false) {
    const trimmed = String(title || '').trim();
    if (!trimmed) throw new Error('Title required');
    const info = await CreateSharedDrive(trimmed, Boolean(requireApproval));
    await loadChannels();
    if (info && Number(info.id)) {
        await switchActiveChannel(Number(info.id));
    }
    return info; // includes invite_link for the share modal
}

export async function joinSharedDrive(link: any) {
    const trimmed = String(link || '').trim();
    if (!trimmed) throw new Error('Invite link required');
    const result = await JoinSharedDrive(trimmed);
    await loadChannels();
    if (result?.status === 'joined' && result?.channel && Number(result.channel.id)) {
        await switchActiveChannel(Number(result.channel.id));
    }
    return result;
}

export async function getInviteLink(channelID: any) {
    return GetInviteLink(Number(channelID || 0));
}

export async function getApprovalInviteLink(channelID: any) {
    return GetApprovalInviteLink(Number(channelID || 0));
}

export async function checkPendingJoin(inviteHash: any) {
    const result = await CheckPendingJoin(String(inviteHash || ''));
    await loadChannels();
    if (result?.status === 'joined' && result?.channel && Number(result.channel.id)) {
        await switchActiveChannel(Number(result.channel.id));
    }
    return result;
}

export async function removePendingJoin(inviteHash: any) {
    await RemovePendingJoin(String(inviteHash || ''));
    await loadChannels();
}

export async function listJoinRequests(channelID: any) {
    return ListJoinRequests(Number(channelID || 0));
}

export async function approveJoinRequest(channelID: any, userID: any) {
    await ApproveJoinRequest(Number(channelID || 0), Number(userID || 0));
}

export async function rejectJoinRequest(channelID: any, userID: any) {
    await RejectJoinRequest(Number(channelID || 0), Number(userID || 0));
}

export async function leaveSharedDrive(channelID: any) {
    if (!channelID) throw new Error('Channel id required');
    await LeaveSharedDrive(Number(channelID));
    await loadChannels();
    if (state.activeChannel) {
        await switchActiveChannel(state.activeChannel.id);
    }
}

export async function switchActiveChannel(channelID: any) {
    if (!channelID) return;
    if (state.channelSwitchInProgress) return;
    state.channelSwitchInProgress = true;
    try {
        await SetActiveChannel(Number(channelID));

        const target = state.channels.find((c) => Number(c?.id) === Number(channelID));
        state.activeChannel = target
            ? { id: Number(target.id), title: String(target.title || ''), kind: String(target.kind || '') }
            : null;
        for (const c of state.channels) {
            if (c) c.is_active = Number(c.id) === Number(channelID);
        }
        applyDriveKindUI();

        // Reset folder/file view state — different drive, different tree.
        state.currentFolderId = '';
        state.folderPath = [];
        state.virtualView = null;
        resetFolderCaches();
        resetSelection();

        renderSidebar();
        await refreshFilesView();

        // Background incremental sync. Don't await — UI feels snappier
        // showing the local cache immediately and folding new ops in
        // when they arrive.
        syncInBackground(channelID);
    } catch (err) {
        console.error('SetActiveChannel failed:', err);
    } finally {
        state.channelSwitchInProgress = false;
    }
}

// refreshActiveDrive is the manual-Refresh entrypoint. It runs an
// incremental sync against the active channel, then re-renders. Awaitable
// — caller can show progress UI and react when done. Sync errors are
// logged and swallowed; the UI re-render still happens so users see
// whatever local state we have.
export async function refreshActiveDrive() {
    if (!state.activeChannel) {
        await refreshFilesView();
        return;
    }
    try {
        await SyncChannel(Number(state.activeChannel.id));
    } catch (err) {
        console.warn('SyncChannel:', err);
    }
    await refreshFilesView();
}

// syncInBackground is fire-and-forget. Used by switchActiveChannel where
// the user has already seen the local cache; we just want to fold in any
// new ops asynchronously.
function syncInBackground(channelID: any) {
    SyncChannel(Number(channelID))
        .then(() => refreshFilesView())
        .catch((err) => console.warn('SyncChannel:', err));
}
