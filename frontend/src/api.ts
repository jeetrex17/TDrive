// Typed boundary over the generated Wails bindings.
//
// Every read that returns drive data goes through here so the snake_case Go
// payloads are normalized into the camelCase types in `types.ts` exactly once.
// UI modules should import from this module instead of calling the raw
// `wailsjs/go/main/App` functions directly.

import {
    CloseMedia as rawCloseMedia,
    GetFolderContents as rawGetFolderContents,
    GetFileList as rawGetFileList,
    ListMedia as rawListMedia,
    OpenMedia as rawOpenMedia,
    Search as rawSearch,
    Thumbnail as rawThumbnail,
} from "../wailsjs/go/main/App";
import type { backend, main, media } from "../wailsjs/go/models";
import type {
    FileItem,
    FolderItem,
    FolderContents,
    RootFile,
    SearchHit,
    SearchHitType,
} from "./types";

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
    name: string;
    info: MediaOpenInfo;
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
    const info: media.LogicalFile | undefined = opened?.info;
    return {
        token: String(opened?.token ?? ""),
        url: String(opened?.url ?? ""),
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

/** Release the media session and its range-reader cache. */
export async function closeMedia(token: string): Promise<void> {
    if (!token) return;
    await rawCloseMedia(token);
}
