// Drive (channel) management — Step 4 shared drives.
//
// Wraps the Wails methods exposed by app_channels.go and the existing
// SetActiveChannel / SyncChannel methods. Keeps state.channels and
// state.activeChannel in sync; tells the sidebar to re-render on every
// change.

import { state, invalidateFolderIndex, resetFolderCaches, resetSelection } from '../state';
import {
    approveJoinRequest as approveJoinRequestApi,
    checkPendingJoin as checkPendingJoinApi,
    createSharedDrive as createSharedDriveApi,
    getApprovalInviteLink as getApprovalInviteLinkApi,
    getInviteLink as getInviteLinkApi,
    joinSharedDrive as joinSharedDriveApi,
    leaveSharedDrive as leaveSharedDriveApi,
    listChannels,
    listJoinRequests as listJoinRequestsApi,
    listPendingJoins,
    onRuntimeEvent,
    rejectJoinRequest as rejectJoinRequestApi,
    removePendingJoin as removePendingJoinApi,
    runtimeEventsAvailable,
    setActiveChannel,
    syncChannel,
} from '../api';
import type { DriveChannel, JoinDriveResult, JoinRequest, PendingJoin } from '../types';
import { runGlobalSearch } from './search';
import type { RefreshFilesOptions } from './app-actions';

interface ChannelRenderers {
    onSidebarUpdate: () => void;
    onActiveDriveChanged: (options?: RefreshFilesOptions) => void | Promise<void>;
}



let renderSidebar: () => void = () => undefined;
let refreshFilesView: (options?: RefreshFilesOptions) => void | Promise<void> = () => undefined;
const pendingLiveSyncChannels = new Set<number>();
let processingLiveSyncRefresh = false;
let disconnectLiveSyncEvents: (() => void) | null = null;

export function bindChannelsRenderers({ onSidebarUpdate, onActiveDriveChanged }: ChannelRenderers): void {
    renderSidebar = onSidebarUpdate;
    refreshFilesView = onActiveDriveChanged;
}

function invalidateDriveCaches(channelId: number): void {
    invalidateFolderIndex(channelId);
    if (state.telegramRootCacheDriveKey !== String(channelId)) return;
    state.telegramRootCache = null;
    state.telegramRootCacheDriveKey = null;
}

function applyChannels(channels: DriveChannel[]): void {
    state.channels = channels;
    const active = channels.find((channel) => channel.isActive)
        ?? channels.find((channel) => channel.kind === 'personal')
        ?? null;
    state.activeChannel = active
        ? { id: active.id, title: active.title, kind: active.kind }
        : null;
}

function applyPendingJoins(pending: PendingJoin[]): void {
    state.pendingJoins = pending;
}

export async function loadChannels(): Promise<void> {
    try {
        const [channels, pending] = await Promise.all([
            listChannels(),
            listPendingJoins().catch((error: unknown) => {
                console.warn('ListPendingJoins failed:', error);
                return [];
            }),
        ]);
        applyChannels(channels);
        applyPendingJoins(pending);
        renderSidebar();
    } catch (error) {
        console.error('ListChannels failed:', error);
        state.channels = [];
        state.activeChannel = null;
        state.pendingJoins = [];
        renderSidebar();
    }
}

export function activateLiveSyncEvents(): () => void {
    disconnectLiveSyncEvents?.();
    if (!runtimeEventsAvailable()) return () => {};

    const stopCompleted = onRuntimeEvent('live_sync_completed', (payload) => {
        queueLiveSyncRefresh(payload);
    });
    const stopFailed = onRuntimeEvent('live_sync_failed', (payload) => {
        const channelId = liveSyncChannelId(payload);
        const activeId = Number(state.activeChannel?.id ?? 0);
        if (channelId && channelId !== activeId) return;
        console.warn('live sync failed:', liveSyncFailure(payload));
    });
    const disconnect = () => {
        if (disconnectLiveSyncEvents !== disconnect) return;
        disconnectLiveSyncEvents = null;
        stopCompleted();
        stopFailed();
        pendingLiveSyncChannels.clear();
    };
    disconnectLiveSyncEvents = disconnect;
    return disconnect;
}

function liveSyncChannelId(payload: unknown): number {
    if (payload === null || typeof payload !== "object" || !("channel_id" in payload)) return 0;
    return Number(payload.channel_id ?? 0);
}

function liveSyncFailure(payload: unknown): unknown {
    if (payload === null || typeof payload !== "object" || !("error" in payload)) return payload;
    return payload.error;
}

function queueLiveSyncRefresh(payload: unknown): void {
    const channelId = liveSyncChannelId(payload);
    if (!channelId) return;
    pendingLiveSyncChannels.add(channelId);
    if (processingLiveSyncRefresh) return;
    processingLiveSyncRefresh = true;
    void processLiveSyncRefreshes();
}

async function processLiveSyncRefreshes(): Promise<void> {
    try {
        while (pendingLiveSyncChannels.size > 0) {
            const changedChannels = new Set(pendingLiveSyncChannels);
            pendingLiveSyncChannels.clear();

            await loadChannels();
            for (const changedId of changedChannels) invalidateDriveCaches(changedId);
            const activeId = Number(state.activeChannel?.id ?? 0);
            if (!activeId || !changedChannels.has(activeId)) continue;

            if (state.searchQuery.trim()) {
                void runGlobalSearch();
            } else {
                await refreshFilesView({ background: true });
            }
        }
    } finally {
        processingLiveSyncRefresh = false;
    }
}

export async function createSharedDrive(title: string, requireApproval = false): Promise<DriveChannel> {
    const trimmed = title.trim();
    if (!trimmed) throw new Error('Title required');
    const info = await createSharedDriveApi(trimmed, requireApproval);
    await loadChannels();
    if (info.id) await switchActiveChannel(info.id);
    return info;
}

export async function joinSharedDrive(link: string): Promise<JoinDriveResult> {
    const trimmed = link.trim();
    if (!trimmed) throw new Error('Invite link required');
    const result = await joinSharedDriveApi(trimmed);
    await loadChannels();
    if (result.status === 'joined' && result.channel?.id) {
        await switchActiveChannel(result.channel.id);
    }
    return result;
}

export function getInviteLink(channelId: number): Promise<string> {
    return getInviteLinkApi(channelId);
}

export function getApprovalInviteLink(channelId: number): Promise<string> {
    return getApprovalInviteLinkApi(channelId);
}

export async function checkPendingJoin(inviteHash: string): Promise<JoinDriveResult> {
    const result = await checkPendingJoinApi(inviteHash);
    await loadChannels();
    if (result.status === 'joined' && result.channel?.id) {
        await switchActiveChannel(result.channel.id);
    }
    return result;
}

export async function removePendingJoin(inviteHash: string): Promise<void> {
    await removePendingJoinApi(inviteHash);
    await loadChannels();
}

export function listJoinRequests(channelId: number): Promise<JoinRequest[]> {
    return listJoinRequestsApi(channelId);
}

export function approveJoinRequest(channelId: number, userId: number): Promise<void> {
    return approveJoinRequestApi(channelId, userId);
}

export function rejectJoinRequest(channelId: number, userId: number): Promise<void> {
    return rejectJoinRequestApi(channelId, userId);
}

export async function leaveSharedDrive(channelId: number): Promise<void> {
    if (!channelId) throw new Error('Channel id required');
    await leaveSharedDriveApi(channelId);
    await loadChannels();
    if (state.activeChannel) await switchActiveChannel(state.activeChannel.id);
}

export async function switchActiveChannel(channelId: number): Promise<void> {
    if (!channelId || state.channelSwitchInProgress) return;
    state.channelSwitchInProgress = true;
    // Route intent changes immediately. A later Photos click must win while the
    // native channel switch is still in flight.
    state.virtualView = null;
    try {
        await setActiveChannel(channelId);

        const target = state.channels.find((channel) => channel.id === channelId) ?? null;
        state.activeChannel = target
            ? { id: target.id, title: target.title, kind: target.kind }
            : null;
        for (const channel of state.channels) {
            channel.isActive = channel.id === channelId;
        }

        state.currentFolderId = '';
        state.folderPath = [];
        invalidateDriveCaches(channelId);
        resetFolderCaches();
        resetSelection();

        renderSidebar();
        await refreshFilesView();
        syncInBackground(channelId);
    } catch (error) {
        console.error('SetActiveChannel failed:', error);
    } finally {
        state.channelSwitchInProgress = false;
    }
}

export async function refreshActiveDrive(): Promise<void> {
    if (!state.activeChannel) {
        await refreshFilesView();
        return;
    }
    const channelId = state.activeChannel.id;
    try {
        await syncChannel(channelId);
    } catch (error) {
        console.warn('SyncChannel:', error);
    } finally {
        invalidateDriveCaches(channelId);
    }
    await refreshFilesView();
}

function syncInBackground(channelId: number): void {
    void syncChannel(channelId)
        .then(() => {
            invalidateDriveCaches(channelId);
            if (Number(state.activeChannel?.id ?? 0) !== channelId) return;
            return refreshFilesView({ background: true });
        })
        .catch((error: unknown) => {
            invalidateDriveCaches(channelId);
            console.warn('SyncChannel:', error);
        });
}
