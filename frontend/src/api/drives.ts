import {
    ApproveJoinRequest as rawApproveJoinRequest,
    CheckPendingJoin as rawCheckPendingJoin,
    CreateSharedDrive as rawCreateSharedDrive,
    GetApprovalInviteLink as rawGetApprovalInviteLink,
    GetInviteLink as rawGetInviteLink,
    JoinSharedDrive as rawJoinSharedDrive,
    LeaveSharedDrive as rawLeaveSharedDrive,
    ListChannels as rawListChannels,
    ListJoinRequests as rawListJoinRequests,
    ListPendingJoins as rawListPendingJoins,
    RejectJoinRequest as rawRejectJoinRequest,
    RemovePendingJoin as rawRemovePendingJoin,
    SetActiveChannel as rawSetActiveChannel,
    SyncChannel as rawSyncChannel,
} from "../../wailsjs/go/main/App";
import type { DriveChannel, JoinDriveResult, JoinRequest, PendingJoin } from "../types";
import { asRecord, finiteNumber } from "./shared";
import { invokeBackend } from "./gateway";

function normalizeDriveKind(value: unknown): DriveChannel["kind"] {
    return value === "personal" || value === "shared" ? value : "unknown";
}

export function normalizeDriveChannel(value: unknown): DriveChannel {
    const raw = asRecord(value);
    return {
        id: finiteNumber(raw.id),
        title: String(raw.title ?? ""),
        kind: normalizeDriveKind(raw.kind),
        isActive: Boolean(raw.is_active),
        inviteLink: String(raw.invite_link ?? ""),
    };
}

export function normalizePendingJoin(value: unknown): PendingJoin {
    const raw = asRecord(value);
    return {
        inviteHash: String(raw.invite_hash ?? ""),
        inviteLink: String(raw.invite_link ?? ""),
        title: String(raw.title ?? ""),
        requestedAt: finiteNumber(raw.requested_at),
        lastCheckedAt: finiteNumber(raw.last_checked_at),
        status: String(raw.status ?? ""),
        lastError: String(raw.last_error ?? ""),
    };
}

export function normalizeJoinDriveResult(value: unknown): JoinDriveResult {
    const raw = asRecord(value);
    return {
        status: String(raw.status ?? ""),
        channel: raw.channel == null ? null : normalizeDriveChannel(raw.channel),
        pending: raw.pending == null ? null : normalizePendingJoin(raw.pending),
    };
}

export function normalizeJoinRequest(value: unknown): JoinRequest {
    const raw = asRecord(value);
    return {
        userId: finiteNumber(raw.user_id),
        displayName: String(raw.display_name ?? ""),
        username: String(raw.username ?? ""),
        requestedAt: finiteNumber(raw.requested_at),
        about: String(raw.about ?? ""),
    };
}
export async function listChannels(): Promise<DriveChannel[]> {
    const channels = await invokeBackend(rawListChannels);
    return (channels ?? []).map(normalizeDriveChannel);
}

export async function createSharedDrive(title: string, requireApproval: boolean): Promise<DriveChannel> {
    return normalizeDriveChannel(await invokeBackend(rawCreateSharedDrive, title, requireApproval));
}

export async function joinSharedDrive(inviteLink: string): Promise<JoinDriveResult> {
    return normalizeJoinDriveResult(await invokeBackend(rawJoinSharedDrive, inviteLink));
}

export async function getInviteLink(channelId: number): Promise<string> {
    return invokeBackend(rawGetInviteLink, channelId);
}

export async function getApprovalInviteLink(channelId: number): Promise<string> {
    return invokeBackend(rawGetApprovalInviteLink, channelId);
}

export async function leaveSharedDrive(channelId: number): Promise<void> {
    await invokeBackend(rawLeaveSharedDrive, channelId);
}

export async function listPendingJoins(): Promise<PendingJoin[]> {
    const pending = await invokeBackend(rawListPendingJoins);
    return (pending ?? []).map(normalizePendingJoin);
}

export async function checkPendingJoin(inviteHash: string): Promise<JoinDriveResult> {
    return normalizeJoinDriveResult(await invokeBackend(rawCheckPendingJoin, inviteHash));
}

export async function removePendingJoin(inviteHash: string): Promise<void> {
    await invokeBackend(rawRemovePendingJoin, inviteHash);
}

export async function listJoinRequests(channelId: number): Promise<JoinRequest[]> {
    const requests = await invokeBackend(rawListJoinRequests, channelId);
    return (requests ?? []).map(normalizeJoinRequest);
}

export async function approveJoinRequest(channelId: number, userId: number): Promise<void> {
    await invokeBackend(rawApproveJoinRequest, channelId, userId);
}

export async function rejectJoinRequest(channelId: number, userId: number): Promise<void> {
    await invokeBackend(rawRejectJoinRequest, channelId, userId);
}

export async function setActiveChannel(channelId: number): Promise<void> {
    await invokeBackend(rawSetActiveChannel, channelId);
}

export async function syncChannel(channelId: number): Promise<void> {
    await invokeBackend(rawSyncChannel, channelId);
}