// Drive (channel) management — Step 4 shared drives.
//
// Wraps the Wails methods exposed by app_channels.go and the existing
// SetActiveChannel / SyncChannel methods. Keeps state.channels and
// state.activeChannel in sync; tells the sidebar to re-render on every
// change.

import { state, resetFolderCaches, resetSelection } from '../state.js';
import {
    ListChannels,
    CreateSharedDrive,
    JoinSharedDrive,
    GetInviteLink,
    LeaveSharedDrive,
    SetActiveChannel,
    SyncChannel,
} from '../../wailsjs/go/main/App';

let renderSidebar = () => {};
let refreshFilesView = () => {};

export function bindChannelsRenderers({ onSidebarUpdate, onActiveDriveChanged }) {
    if (typeof onSidebarUpdate === 'function') renderSidebar = onSidebarUpdate;
    if (typeof onActiveDriveChanged === 'function') refreshFilesView = onActiveDriveChanged;
}

function applyChannels(list) {
    const arr = Array.isArray(list) ? list : [];
    state.channels = arr;
    const active = arr.find((c) => c && c.is_active) || arr.find((c) => c && c.kind === 'personal') || null;
    state.activeChannel = active
        ? { id: Number(active.id), title: String(active.title || ''), kind: String(active.kind || '') }
        : null;
    applyDriveKindUI();
    renderSidebar();
}

// Show the shared-drive banner and hide folder-related controls when the
// active drive is shared. Step 4 decision: shared drives are flat-file only.
function applyDriveKindUI() {
    const isShared = state.activeChannel?.kind === 'shared';
    const banner = document.getElementById('shared-drive-banner');
    if (banner) banner.style.display = isShared ? 'flex' : 'none';
    const folderBtn = document.getElementById('new-folder-btn');
    if (folderBtn) folderBtn.style.display = isShared ? 'none' : '';
    const moveBtn = document.getElementById('selection-move');
    if (moveBtn) moveBtn.style.display = isShared ? 'none' : '';
}

export async function loadChannels() {
    try {
        const list = await ListChannels();
        applyChannels(list);
    } catch (err) {
        console.error('ListChannels failed:', err);
        state.channels = [];
        state.activeChannel = null;
        renderSidebar();
    }
}

export async function createSharedDrive(title) {
    const trimmed = String(title || '').trim();
    if (!trimmed) throw new Error('Title required');
    const info = await CreateSharedDrive(trimmed);
    await loadChannels();
    if (info && Number(info.id)) {
        await switchActiveChannel(Number(info.id));
    }
    return info; // includes invite_link for the share modal
}

export async function joinSharedDrive(link) {
    const trimmed = String(link || '').trim();
    if (!trimmed) throw new Error('Invite link required');
    const info = await JoinSharedDrive(trimmed);
    await loadChannels();
    if (info && Number(info.id)) {
        await switchActiveChannel(Number(info.id));
    }
    return info;
}

export async function getInviteLink(channelID) {
    return GetInviteLink(Number(channelID || 0));
}

export async function leaveSharedDrive(channelID) {
    if (!channelID) throw new Error('Channel id required');
    await LeaveSharedDrive(Number(channelID));
    await loadChannels();
    if (state.activeChannel) {
        await switchActiveChannel(state.activeChannel.id);
    }
}

export async function switchActiveChannel(channelID) {
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
function syncInBackground(channelID) {
    SyncChannel(Number(channelID))
        .then(() => refreshFilesView())
        .catch((err) => console.warn('SyncChannel:', err));
}
