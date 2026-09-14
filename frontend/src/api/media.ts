import {
    AttachNativeMedia as rawAttachNativeMedia,
    CloseMedia as rawCloseMedia,
    CloseNativeMedia as rawCloseNativeMedia,
    GetMediaStats as rawGetMediaStats,
    HideNativeSeekThumbnail as rawHideNativeSeekThumbnail,
    ListMedia as rawListMedia,
    MoveNativeSeekThumbnail as rawMoveNativeSeekThumbnail,
    NativeMediaCommand as rawNativeMediaCommand,
    OpenMedia as rawOpenMedia,
    OpenNativeMedia as rawOpenNativeMedia,
    OpenStream as rawOpenStream,
    ResizeNativeMedia as rawResizeNativeMedia,
    ShowNativeSeekThumbnail as rawShowNativeSeekThumbnail,
    Thumbnail as rawThumbnail,
    UpdateMediaPlayback as rawUpdateMediaPlayback,
} from "../../bindings/TDrive/app";
import type { LogicalFile, OpenResult, ThroughputStats as MediaThroughputStats } from "../../bindings/TDrive/backend/media/models";
import type { NativeMediaResult } from "../../bindings/TDrive/models";
import type { FileItem } from "../types";
import { toFileItem } from "./files";
import { invokeBackend } from "./gateway";

export interface MediaOpenInfo {
    channelId: number;
    fileId: number;
    revision: number;
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
    presentation: "embedded" | "standalone";
    initialState: NativeMediaStatePayload | null;
    name: string;
    info: MediaOpenInfo;
}

export interface NativeMediaStatePayload {
    token?: string;
    sequence?: number;
    status?: string;
    error?: unknown;
    eof?: boolean;
    paused?: boolean;
    current_time?: number;
    duration?: number;
    buffered?: Array<{ start?: number; end?: number }>;
    volume?: number;
    muted?: boolean;
    rate?: number;
    loading?: boolean;
    tracks?: unknown;
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

/** Every image in the active drive, newest first, for the Photos gallery. */
export async function getMedia(): Promise<FileItem[]> {
    const files = await invokeBackend(rawListMedia);
    return (files ?? []).map(toFileItem);
}

/**
 * A downscaled JPEG thumbnail for one image, as a ready-to-use data URL.
 * Rejects when the backend can't produce one (unsupported, too large, or a
 * locked encrypted drive) so the caller can render the right placeholder.
 */
export async function getThumbnail(msgId: number): Promise<string> {
    const payload = await invokeBackend(rawThumbnail, msgId);
    const dataBase64 = String(payload?.data_base64 ?? "");
    const mimeType = String(payload?.mime_type ?? "");
    if (!dataBase64 || !mimeType) throw new Error("thumbnail unavailable");
    return `data:${mimeType};base64,${dataBase64}`;
}

/** Open a short-lived loopback media URL for a projected file. */
export async function openMedia(msgId: number): Promise<MediaOpenResult> {
    const opened = await invokeBackend(rawOpenMedia, msgId);
    return normalizeMediaOpenResult(opened);
}

/** Open a loopback stream URL for a projected audio/PDF/text file. */
export async function openStream(msgId: number): Promise<MediaOpenResult> {
    const opened = await invokeBackend(rawOpenStream, msgId);
    return normalizeMediaOpenResult(opened);
}

function normalizeMediaOpenResult(opened?: OpenResult): MediaOpenResult {
    return {
        token: String(opened?.token ?? ""),
        url: String(opened?.url ?? ""),
        thumbnailUrl: String(opened?.thumbnail_url ?? ""),
        name: String(opened?.name ?? ""),
        kind: String(opened?.kind ?? ""),
        mimeType: String(opened?.mime_type ?? ""),
        supportsRange: Boolean(opened?.supports_range),
        info: normalizeMediaOpenInfo(opened?.info, opened?.name),
    };
}

function normalizeMediaOpenInfo(info?: LogicalFile, fallbackName?: string): MediaOpenInfo {
    return {
        channelId: Number(info?.channel_id ?? 0),
        fileId: Number(info?.file_id ?? 0),
        revision: Number(info?.revision ?? 0),
        name: String(info?.name ?? fallbackName ?? ""),
        storedSize: Number(info?.stored_size ?? 0),
        plaintextSize: Number(info?.plaintext_size ?? 0),
        encrypted: Boolean(info?.encrypted),
        multipart: Boolean(info?.multipart),
    };
}

/** Release the media session and its range-reader cache. */
export async function closeMedia(token: string): Promise<void> {
    if (!token) return;
    await invokeBackend(rawCloseMedia, token);
}

/** Open a native all-format player for one projected file. */
export async function openNativeMedia(msgId: number, rect: NativeMediaRect): Promise<NativeMediaOpenResult> {
    const opened = await invokeBackend(rawOpenNativeMedia, msgId, rect);
    return normalizeNativeMediaOpenResult(opened);
}

/** Promote an existing webview stream to native playback without reopening it. */
export async function attachNativeMedia(token: string, rect: NativeMediaRect): Promise<NativeMediaOpenResult> {
    if (!token) throw new Error("Media session is required.");
    const opened = await invokeBackend(rawAttachNativeMedia, token, rect);
    return normalizeNativeMediaOpenResult(opened);
}

function normalizeNativeMediaOpenResult(opened?: NativeMediaResult): NativeMediaOpenResult {
    const token = String(opened?.token ?? "");
    const rawInitialState = opened?.initial_state;
    const initialStateRecord = rawInitialState !== null
        && typeof rawInitialState === "object"
        && !Array.isArray(rawInitialState)
        ? rawInitialState as NativeMediaStatePayload
        : null;
    const initialState: NativeMediaStatePayload | null = initialStateRecord
        ? { ...initialStateRecord, token: String(initialStateRecord.token || token) }
        : null;
    return {
        token,
        thumbnailUrl: String(opened?.thumbnail_url ?? ""),
        htmlControls: Boolean(opened?.html_controls),
        presentation: opened?.presentation === "standalone" ? "standalone" : "embedded",
        initialState,
        name: String(opened?.name ?? ""),
        info: normalizeMediaOpenInfo(opened?.info, opened?.name),
    };
}

export async function resizeNativeMedia(token: string, rect: NativeMediaRect): Promise<void> {
    if (!token) return;
    await invokeBackend(rawResizeNativeMedia, token, rect);
}

export async function nativeMediaCommand(token: string, command: string[]): Promise<void> {
    if (!token || command.length === 0) return;
    await invokeBackend(rawNativeMediaCommand, token, command);
}

export async function closeNativeMedia(token: string): Promise<void> {
    if (!token) return;
    await invokeBackend(rawCloseNativeMedia, token);
}

// Windows paints seek previews above its child mpv window because the webview
// cannot layer HTML over that native surface. Other players deliberately no-op.
// imageBase64 is a JPEG/PNG frame; rect is the preview box in CSS pixels.
export async function showNativeSeekThumbnail(token: string, imageBase64: string, rect: NativeMediaRect): Promise<void> {
    if (!token || !imageBase64) return;
    await invokeBackend(rawShowNativeSeekThumbnail, token, imageBase64, rect);
}

export async function moveNativeSeekThumbnail(token: string, rect: NativeMediaRect): Promise<void> {
    if (!token) return;
    await invokeBackend(rawMoveNativeSeekThumbnail, token, rect);
}

export async function hideNativeSeekThumbnail(token: string): Promise<void> {
    if (!token) return;
    await invokeBackend(rawHideNativeSeekThumbnail, token);
}

export async function updateMediaPlayback(update: MediaPlaybackUpdate): Promise<void> {
    if (!update.token) return;
    await invokeBackend(rawUpdateMediaPlayback, {
        token: update.token,
        current_time: update.currentTime,
        duration: update.duration,
        buffer_ahead: update.bufferAhead,
    });
}

function toThroughputStats(stats?: MediaThroughputStats): ThroughputStats {
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
    const stats = await invokeBackend(rawGetMediaStats, token);
    return {
        playback: toThroughputStats(stats?.playback),
        thumbnails: toThroughputStats(stats?.thumbnails),
    };
}