import {
    CheckLoginStatus as rawCheckLoginStatus,
    CheckSystemStatus as rawCheckSystemStatus,
    CreatePersonalDrive as rawCreatePersonalDrive,
    DiscoverPersonalDrives as rawDiscoverPersonalDrives,
    LoginPhoneNumber as rawLoginPhoneNumber,
    Logout as rawLogout,
    Me as rawMe,
    MyUserID as rawMyUserId,
    PreparePersonalDrive as rawPreparePersonalDrive,
    ResolveUsernames as rawResolveUsernames,
    SaveSetup as rawSaveSetup,
    SelectPersonalDrive as rawSelectPersonalDrive,
    SubmitCode as rawSubmitCode,
    SubmitPassword as rawSubmitPassword,
} from "../../wailsjs/go/main/App";
import type { PersonalDriveCandidate, PersonalDriveSetup, SelfUser } from "../types";
import { asRecord, finiteNumber } from "./shared";

function normalizePersonalDriveCandidate(value: unknown): PersonalDriveCandidate {
    const raw = asRecord(value);
    return {
        id: String(raw.id ?? ""),
        title: String(raw.title ?? ""),
        createdAt: finiteNumber(raw.created_at),
        hasActivity: Boolean(raw.has_activity),
        recommended: Boolean(raw.recommended),
    };
}

function normalizePersonalDriveSetup(value: unknown): PersonalDriveSetup {
    const raw = asRecord(value);
    return {
        status: String(raw.status ?? ""),
        activeChannelId: String(raw.active_channel_id ?? ""),
    };
}

export function normalizeSelfUser(value: unknown): SelfUser {
    const raw = asRecord(value);
    return {
        userId: finiteNumber(raw.user_id),
        displayName: String(raw.display_name ?? ""),
        username: String(raw.username ?? ""),
        photoBase64: String(raw.photo_base64 ?? ""),
    };
}
export async function checkSystemStatus(): Promise<string> {
    return rawCheckSystemStatus();
}

export async function saveSetup(apiId: number, apiHash: string): Promise<string> {
    return rawSaveSetup(apiId, apiHash);
}

export async function loginPhoneNumber(phone: string): Promise<void> {
    await rawLoginPhoneNumber(phone);
}

export async function submitCode(code: string): Promise<void> {
    await rawSubmitCode(code);
}

export async function submitPassword(password: string): Promise<void> {
    await rawSubmitPassword(password);
}

export async function checkLoginStatus(): Promise<boolean> {
    return rawCheckLoginStatus();
}

export async function preparePersonalDrive(): Promise<PersonalDriveSetup> {
    return normalizePersonalDriveSetup(await rawPreparePersonalDrive());
}

export async function discoverPersonalDrives(): Promise<PersonalDriveCandidate[]> {
    const drives = await rawDiscoverPersonalDrives();
    return (drives ?? []).map(normalizePersonalDriveCandidate);
}

export async function selectPersonalDrive(channelId: string): Promise<void> {
    await rawSelectPersonalDrive(channelId);
}

export async function createPersonalDrive(): Promise<void> {
    await rawCreatePersonalDrive();
}

export async function getMyUserId(): Promise<number> {
    return rawMyUserId();
}
export async function logout(mode: string): Promise<void> {
    await rawLogout(mode);
}

export async function getSelfUser(): Promise<SelfUser> {
    return normalizeSelfUser(await rawMe());
}

export async function resolveUsernames(userIds: number[]): Promise<Record<string, string>> {
    const resolved = await rawResolveUsernames(userIds);
    return Object.fromEntries(Object.entries(resolved ?? {}).map(([id, name]) => [id, String(name)]));
}