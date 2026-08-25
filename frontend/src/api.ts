// Typed boundary over the generated Wails bindings.
//
// Every read that returns drive data goes through here so the snake_case Go
// payloads are normalized into the camelCase types in `types.ts` exactly once.
// UI modules should import from this module instead of calling the raw
// `wailsjs/go/main/App` functions directly.

import {
    CloseMedia as rawCloseMedia,
    CloseNativeMedia as rawCloseNativeMedia,
    GetFolderContents as rawGetFolderContents,
    GetFileList as rawGetFileList,
    GetMediaStats as rawGetMediaStats,
    HideNativeSeekThumbnail as rawHideNativeSeekThumbnail,
    ListChannels as rawListChannels,
    ListMedia as rawListMedia,
    MountDrive as rawMountDrive,
    MountDrives as rawMountDrives,
    MountStatus as rawMountStatus,
    MoveNativeSeekThumbnail as rawMoveNativeSeekThumbnail,
    NativeMediaCommand as rawNativeMediaCommand,
    OpenMedia as rawOpenMedia,
    OpenNativeMedia as rawOpenNativeMedia,
    OpenStream as rawOpenStream,
    ResizeNativeMedia as rawResizeNativeMedia,
    Search as rawSearch,
    ShowNativeSeekThumbnail as rawShowNativeSeekThumbnail,
    Thumbnail as rawThumbnail,
    UnmountDrive as rawUnmountDrive,
    UpdateMediaPlayback as rawUpdateMediaPlayback,
} from "../wailsjs/go/main/App";
import type { backend, main, media } from "../wailsjs/go/models";
import type {
    FileItem,
    FolderItem,
    FolderContents,
    RootFile,
    SearchHit,
    SearchHitType,
    MountedDrive,
    MountedDriveKind,
    MountableDrive,
    MountMode,
    MountPhase,
    MountStatusView,
    MountWriteState,
} from "./types";

export const MOUNT_LABEL = 'Tdrive personal' as const;

type UnknownRecord = Record<string, unknown>;

const UNSAFE_MOUNT_DETAIL = /(?:https?:\/\/|webdav:\/\/|dav:\/\/|tdrive-[a-f\d]{8,}|mount_webdav|\bnet\s+use\b|\bgio\s+mount\b)/i;
const LOOPBACK_LOCATION = /(?:127\.0\.0\.1|\blocalhost\b|\[::1\])/i;

function asRecord(value: unknown): UnknownRecord {
    return value !== null && typeof value === 'object' ? value as UnknownRecord : {};
}

function boundedText(value: unknown, maxLength: number): string {
    if (typeof value !== 'string') return '';
    return [...value]
        .filter((character) => {
            const codePoint = character.codePointAt(0) ?? 0;
            return codePoint > 31 && codePoint !== 127;
        })
        .join('')
        .trim()
        .slice(0, maxLength);
}

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

export async function mountDrive(): Promise<MountStatusView> {
    return normalizeMountStatus(await rawMountDrive());
}

export async function listMountableDrives(): Promise<MountableDrive[]> {
    return normalizeMountableDrives(await rawListChannels());
}

export async function mountDrives(channelIds: readonly number[]): Promise<MountStatusView> {
    const selected = [...new Set(channelIds)]
        .filter((id) => Number.isSafeInteger(id) && id > 0);
    if (selected.length === 0) throw new Error('Select at least one drive to mount.');
    return normalizeMountStatus(await rawMountDrives(selected));
}

export async function getMountStatus(): Promise<MountStatusView> {
    return normalizeMountStatus(await rawMountStatus());
}

export async function unmountDrive(): Promise<MountStatusView> {
    return normalizeMountStatus(await rawUnmountDrive());
}

export interface MediaOpenInfo {
    channelId: number;
    fileId: number;
    name: string;
    storedSize: number;
    plaintextSize: number;
    encrypted: boolean;
    multipart: boolean;
}

export interface MediaOpenResult {
    token: string;
    url: string;
    thumbnailUrl: string;
    name: string;
    kind: string;
    mimeType: string;
    supportsRange: boolean;
    info: MediaOpenInfo;
}

export interface NativeMediaRect {
    x: number;
    y: number;
    width: number;
    height: number;
}

export interface NativeMediaOpenResult {
    token: string;
    thumbnailUrl: string;
    htmlControls: boolean;
    name: string;
    info: MediaOpenInfo;
}

export interface MediaPlaybackUpdate {
    token: string;
    currentTime: number;
    duration: number;
    bufferAhead: number;
}

export interface ThroughputStats {
    bytesPerSecond: number;
    recentFloodWait: boolean;
    lastFloodWaitSeconds: number;
}

export interface MediaStats {
    playback: ThroughputStats;
    thumbnails: ThroughputStats;
}

export function toFileItem(f: backend.FileMetaData): FileItem {
    return {
        msgId: Number(f.msg_id ?? 0),
        name: String(f.name ?? ""),
        size: Number(f.size ?? 0),
        parentId: String(f.parent_id ?? ""),
        uploadTime: Number(f.upload_time ?? 0),
        uploaderId: Number(f.uploader_id ?? 0),
        encrypted: Boolean(f.encrypted),
        plaintextSize: Number(f.plaintext_size ?? 0),
    };
}

export function toFolderItem(d: backend.Folder): FolderItem {
    return {
        id: String(d.id ?? ""),
        name: String(d.name ?? ""),
        parentId: String(d.parent_id ?? ""),
    };
}

export function toRootFile(f: main.TDriveFile): RootFile {
    return {
        msgId: Number(f.id ?? 0),
        name: String(f.name ?? ""),
        size: Number(f.size ?? 0),
        accessHash: Number(f.access_hash ?? 0),
        date: Number(f.date ?? 0),
    };
}

export function toSearchHit(h: backend.SearchResult): SearchHit {
    const type: SearchHitType = h.type === "folder" ? "folder" : "file";
    return {
        type,
        id: String(h.id ?? ""),
        name: String(h.name ?? ""),
        parentId: String(h.parent_id ?? ""),
        size: Number(h.size ?? 0),
        uploadTime: Number(h.upload_time ?? 0),
        uploaderId: Number(h.uploader_id ?? 0),
        path: String(h.path ?? ""),
    };
}

/** Subfolders and files under a parent folder, normalized. */
export async function getFolderContents(parentId: string): Promise<FolderContents> {
    const fs = await rawGetFolderContents(parentId);
    return {
        folders: (fs?.folders ?? []).map(toFolderItem),
        files: (fs?.files ?? []).map(toFileItem),
    };
}

/** Flat list of root files read straight from Telegram history, normalized. */
export async function getFileList(): Promise<RootFile[]> {
    const files = await rawGetFileList();
    return (files ?? []).map(toRootFile);
}

/** Search files and folders in the active drive, normalized. */
export async function search(query: string, limit: number): Promise<SearchHit[]> {
    const hits = await rawSearch(query, limit);
    return (hits ?? []).map(toSearchHit);
}

/** Every image in the active drive, newest first, for the Photos gallery. */
export async function getMedia(): Promise<FileItem[]> {
    const files = await rawListMedia();
    return (files ?? []).map(toFileItem);
}

/**
 * A downscaled JPEG thumbnail for one image, as a ready-to-use data URL.
 * Rejects when the backend can't produce one (unsupported, too large, or a
 * locked encrypted drive) so the caller can render the right placeholder.
 */
export async function getThumbnail(msgId: number): Promise<string> {
    const payload = await rawThumbnail(msgId);
    const dataBase64 = String(payload?.data_base64 ?? "");
    const mimeType = String(payload?.mime_type ?? "");
    if (!dataBase64 || !mimeType) throw new Error("thumbnail unavailable");
    return `data:${mimeType};base64,${dataBase64}`;
}

/** Open a short-lived loopback media URL for a projected file. */
export async function openMedia(msgId: number): Promise<MediaOpenResult> {
    const opened = await rawOpenMedia(msgId);
    return normalizeMediaOpenResult(opened);
}

/** Open a loopback stream URL for a projected audio/PDF/text file. */
export async function openStream(msgId: number): Promise<MediaOpenResult> {
    const opened = await rawOpenStream(msgId);
    return normalizeMediaOpenResult(opened);
}

function normalizeMediaOpenResult(opened?: media.OpenResult): MediaOpenResult {
    const info: media.LogicalFile | undefined = opened?.info;
    return {
        token: String(opened?.token ?? ""),
        url: String(opened?.url ?? ""),
        thumbnailUrl: String(opened?.thumbnail_url ?? ""),
        name: String(opened?.name ?? ""),
        kind: String(opened?.kind ?? ""),
        mimeType: String(opened?.mime_type ?? ""),
        supportsRange: Boolean(opened?.supports_range),
        info: {
            channelId: Number(info?.channel_id ?? 0),
            fileId: Number(info?.file_id ?? 0),
            name: String(info?.name ?? opened?.name ?? ""),
            storedSize: Number(info?.stored_size ?? 0),
            plaintextSize: Number(info?.plaintext_size ?? 0),
            encrypted: Boolean(info?.encrypted),
            multipart: Boolean(info?.multipart),
        },
    };
}

/** Release the media session and its range-reader cache. */
export async function closeMedia(token: string): Promise<void> {
    if (!token) return;
    await rawCloseMedia(token);
}

/** Open a native all-format player for one projected file. */
export async function openNativeMedia(msgId: number, rect: NativeMediaRect): Promise<NativeMediaOpenResult> {
    const opened = await rawOpenNativeMedia(msgId, rect as any);
    const info: media.LogicalFile | undefined = opened?.info;
    return {
        token: String(opened?.token ?? ""),
        thumbnailUrl: String(opened?.thumbnail_url ?? ""),
        htmlControls: Boolean(opened?.html_controls),
        name: String(opened?.name ?? ""),
        info: {
            channelId: Number(info?.channel_id ?? 0),
            fileId: Number(info?.file_id ?? 0),
            name: String(info?.name ?? opened?.name ?? ""),
            storedSize: Number(info?.stored_size ?? 0),
            plaintextSize: Number(info?.plaintext_size ?? 0),
            encrypted: Boolean(info?.encrypted),
            multipart: Boolean(info?.multipart),
        },
    };
}

export async function resizeNativeMedia(token: string, rect: NativeMediaRect): Promise<void> {
    if (!token) return;
    await rawResizeNativeMedia(token, rect as any);
}

export async function nativeMediaCommand(token: string, command: string[]): Promise<void> {
    if (!token || command.length === 0) return;
    await rawNativeMediaCommand(token, command);
}

export async function closeNativeMedia(token: string): Promise<void> {
    if (!token) return;
    await rawCloseNativeMedia(token);
}

// showNativeSeekThumbnail paints a seek-preview thumbnail over the native video
// window (Windows/Linux fallback, where HTML can't draw over the video). The
// image is the raw base64 of a JPEG/PNG frame; rect is the preview box in CSS
// pixels. No-op on platforms whose player has no overlay.
export async function showNativeSeekThumbnail(token: string, imageBase64: string, rect: NativeMediaRect): Promise<void> {
    if (!token || !imageBase64) return;
    await rawShowNativeSeekThumbnail(token, imageBase64, rect as any);
}

export async function moveNativeSeekThumbnail(token: string, rect: NativeMediaRect): Promise<void> {
    if (!token) return;
    await rawMoveNativeSeekThumbnail(token, rect as any);
}

export async function hideNativeSeekThumbnail(token: string): Promise<void> {
    if (!token) return;
    await rawHideNativeSeekThumbnail(token);
}

export async function updateMediaPlayback(update: MediaPlaybackUpdate): Promise<void> {
    if (!update.token) return;
    await rawUpdateMediaPlayback({
        token: update.token,
        current_time: update.currentTime,
        duration: update.duration,
        buffer_ahead: update.bufferAhead,
    } as any);
}

function toThroughputStats(stats?: media.ThroughputStats): ThroughputStats {
    return {
        bytesPerSecond: Number(stats?.bytes_per_second ?? 0),
        recentFloodWait: Boolean(stats?.recent_flood_wait),
        lastFloodWaitSeconds: Number(stats?.last_flood_wait_seconds ?? 0),
    };
}

export async function getMediaStats(token: string): Promise<MediaStats> {
    if (!token) {
        return {
            playback: toThroughputStats(),
            thumbnails: toThroughputStats(),
        };
    }
    const stats = await rawGetMediaStats(token);
    return {
        playback: toThroughputStats(stats?.playback),
        thumbnails: toThroughputStats(stats?.thumbnails),
    };
}
