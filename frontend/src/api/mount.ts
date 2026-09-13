import {
    ListChannels as rawListChannels,
    MountDrive as rawMountDrive,
    MountDrives as rawMountDrives,
    MountStatus as rawMountStatus,
    UnmountDrive as rawUnmountDrive,
} from "../../wailsjs/go/main/App";
import type {
    MountedDrive,
    MountedDriveKind,
    MountableDrive,
    MountMode,
    MountPhase,
    MountStatusView,
    MountWriteState,
} from "../types";
import { normalizeOperationResult, requireOperationSuccess } from "./operation";
import { asRecord, boundedText } from "./shared";

export const MOUNT_LABEL = "Tdrive personal" as const;

const UNSAFE_MOUNT_DETAIL = /(?:https?:\/\/|webdav:\/\/|dav:\/\/|tdrive-[a-f\d]{8,}|mount_webdav|\bnet\s+use\b|\bgio\s+mount\b)/i;
const LOOPBACK_LOCATION = /(?:127\.0\.0\.1|\blocalhost\b|\[::1\])/i;

function normalizeMountPhase(value: unknown, mounted: boolean, error: string): MountPhase {
    if (error || value === 'error') return 'error';
    if (value === 'disconnecting') return 'disconnecting';
    if (mounted) return 'mounted';
    return value === 'mounting' ? 'mounting' : 'idle';
}

function normalizeMountedDrive(value: unknown): MountedDrive | null {
    const raw = asRecord(value);
    const rawID = Number(raw.id ?? 0);
    const id = Number.isSafeInteger(rawID) && rawID > 0 ? rawID : 0;
    const title = boundedText(raw.title, 160);
    const rawKind = boundedText(raw.kind, 24);
    const kind: MountedDriveKind = rawKind === 'personal' || rawKind === 'shared'
        ? rawKind
        : 'unknown';
    if (id === 0 && !title && rawKind === '') return null;
    return { id, title, kind };
}

function normalizeMountLocation(value: unknown): string {
    const location = boundedText(value, 320);
    if (UNSAFE_MOUNT_DETAIL.test(location) || LOOPBACK_LOCATION.test(location)) return '';
    return location;
}

function normalizeMountLabel(value: unknown): string {
    const label = boundedText(value, 96);
    if (!label || UNSAFE_MOUNT_DETAIL.test(label) || LOOPBACK_LOCATION.test(label)) return MOUNT_LABEL;
    return label;
}

function normalizeMountMode(value: unknown): MountMode {
    return value === 'read-write' ? 'read-write' : 'read-only';
}

function normalizeMountWriteState(value: unknown, mode: MountMode): MountWriteState {
    if (mode === 'read-only') return 'disabled';
    if (value === 'starting' || value === 'ready' || value === 'draining' || value === 'drained') {
        return value;
    }
    return 'starting';
}

function normalizeActiveWrites(value: unknown, mode: MountMode): number {
    if (mode === 'read-only') return 0;
    const count = Number(value ?? 0);
    return Number.isSafeInteger(count) && count >= 0 && count <= 1024 ? count : 0;
}

/** Converts backend/bridge failures into endpoint-free user-facing text. */
export function safeMountError(value: unknown, fallback = 'The mount operation failed. Try again.'): string {
    const message = value instanceof Error
        ? boundedText(value.message, 240)
        : boundedText(value, 240);
    if (!message || UNSAFE_MOUNT_DETAIL.test(message) || LOOPBACK_LOCATION.test(message)) return fallback;
    return message;
}

/** Normalize the Go DTO and intentionally drop endpoint URLs and command hints. */
export function normalizeMountStatus(value: unknown): MountStatusView {
    const raw = asRecord(value);
    const mounted = Boolean(raw.mounted);
    const error = safeMountError(raw.error, 'The drive could not be mounted. Try again.');
    const hasError = boundedText(raw.error, 1) !== '';
    const mode = normalizeMountMode(raw.mode);
    const writeState = normalizeMountWriteState(raw.write_state, mode);
    return {
        phase: normalizeMountPhase(raw.phase, mounted, hasError ? error : ''),
        mounted,
        mode,
        writeState,
        acceptingWrites: mounted && mode === 'read-write' && writeState === 'ready' && raw.accepting_writes === true,
        activeWrites: normalizeActiveWrites(raw.active_writes, mode),
        label: normalizeMountLabel(raw.label),
        location: normalizeMountLocation(raw.location),
        error: hasError ? error : '',
        drive: normalizeMountedDrive(raw.drive),
    };
}

/** Normalize the channel list before exposing it to the mount picker. */
export function normalizeMountableDrives(value: unknown): MountableDrive[] {
    if (!Array.isArray(value)) return [];

    const seen = new Set<number>();
    const drives = value.flatMap((entry): MountableDrive[] => {
        const raw = asRecord(entry);
        const id = Number(raw.id ?? 0);
        const kind = raw.kind === 'personal' || raw.kind === 'shared' ? raw.kind : null;
        if (!Number.isSafeInteger(id) || id <= 0 || !kind || seen.has(id)) return [];

        seen.add(id);
        return [{
            id,
            title: boundedText(raw.title, 160) || (kind === 'personal' ? 'Personal' : 'Shared drive'),
            kind,
        }];
    });

    return [...drives].sort((left, right) => {
        if (left.kind === right.kind) return 0;
        return left.kind === 'personal' ? -1 : 1;
    });
}

export function normalizeMountResult(value: unknown): MountStatusView {
    const raw = asRecord(value);
    requireOperationSuccess(normalizeOperationResult(raw.result, 'The drive could not be mounted.'));
    return normalizeMountStatus(raw.mount);
}

export async function mountDrive(): Promise<MountStatusView> {
    return normalizeMountResult(await rawMountDrive());
}

export async function listMountableDrives(): Promise<MountableDrive[]> {
    return normalizeMountableDrives(await rawListChannels());
}

export async function mountDrives(channelIds: readonly number[]): Promise<MountStatusView> {
    const selected = [...new Set(channelIds)]
        .filter((id) => Number.isSafeInteger(id) && id > 0);
    if (selected.length === 0) throw new Error('Select at least one drive to mount.');
    return normalizeMountResult(await rawMountDrives(selected));
}

export async function getMountStatus(): Promise<MountStatusView> {
    return normalizeMountStatus(await rawMountStatus());
}

export async function unmountDrive(): Promise<MountStatusView> {
    return normalizeMountStatus(await rawUnmountDrive());
}
