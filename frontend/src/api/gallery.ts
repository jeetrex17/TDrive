import {
    GetMediaFolderTimeline, GetMediaTimeline, GetMediaTimelineAnchors, GetMediaTimelineSummary,
    ListMediaFolderPage, ListMediaFolders, ListMediaPage, LocateMedia,
} from '../../bindings/TDrive/app';
import type { FileItem } from '../types';
import { invokeBackend } from './gateway';
import { asRecord, boundedText } from './shared';

export interface GalleryItem extends FileItem {
    revision: number;
    contentMsgId: number;
    contentHash: string;
}
export interface MediaBucket { key: string; startIndex: number; count: number; uploadTime: number }
export interface MediaAnchor { startIndex: number; cursor: string }
export interface MediaTimeline {
    channelId: number;
    generation: string;
    totalCount: number;
    pageSize: number;
    buckets: MediaBucket[];
    anchors: MediaAnchor[];
}
export interface MediaPage { generation: string; startIndex: number; items: GalleryItem[]; nextCursor: string }
export interface MediaLocation { generation: string; index: number; cursor: string }

/**
 * One folder that directly holds media: the album grid's row. The backend
 * already orders these newest-first, and the grid renders that order as it
 * arrives -- re-sorting here would only be a second opinion about the same
 * timestamps.
 */
export interface MediaFolder {
    /** Drive folder id, or '' for the drive's own root. */
    folderId: string;
    /** '' for the root, which has no name of its own; the view names it. */
    name: string;
    /** Media directly in this folder, never in its subfolders. */
    itemCount: number;
    latestUploadTime: number;
    /** The newest item, shown as the cover. Zero when there is nothing to show. */
    coverMsgId: number;
    coverRevision: number;
    coverName: string;
}

// Generous enough for any real folder or file name, hard enough that a
// malformed row cannot push an unbounded string into the DOM.
const MAX_FOLDER_ID = 128;
const MAX_NAME = 512;

function integer(value: unknown, minimum = 0): number {
    const number = Number(value);
    if (!Number.isSafeInteger(number) || number < minimum) throw new Error('Invalid gallery metadata');
    return number;
}

export function normalizeMediaTimeline(value: unknown, allowMissingAnchors = false): MediaTimeline {
    const raw = asRecord(value);
    const totalCount = integer(raw.total_count);
    const pageSize = integer(raw.page_size, 1);
    if (pageSize > 256 || !Array.isArray(raw.buckets) || !Array.isArray(raw.anchors)) throw new Error('Invalid gallery timeline');
    let position = 0;
    const buckets = raw.buckets.map((value) => {
        const entry = asRecord(value);
        const bucket = { key: String(entry.key ?? ''), startIndex: integer(entry.start_index), count: integer(entry.count, 1), uploadTime: integer(entry.upload_time) };
        if (bucket.startIndex !== position) throw new Error('Invalid gallery bucket ordering');
        position += bucket.count;
        return bucket;
    });
    const anchors = raw.anchors.map((value, index) => {
        const entry = asRecord(value);
        const anchor = { startIndex: integer(entry.start_index), cursor: String(entry.cursor ?? '') };
        if (anchor.startIndex !== index * pageSize || !anchor.cursor) throw new Error('Invalid gallery page anchor');
        return anchor;
    });
    if (position !== totalCount || (!allowMissingAnchors && anchors.length !== Math.ceil(totalCount / pageSize))
        || (allowMissingAnchors && anchors.length !== 0 && anchors.length !== Math.ceil(totalCount / pageSize))) throw new Error('Incomplete gallery timeline');
    return { channelId: integer(raw.channel_id, 1), generation: String(raw.generation ?? ''), totalCount, pageSize, buckets, anchors };
}

export function normalizeMediaPage(value: unknown): MediaPage {
    const raw = asRecord(value);
    if (!Array.isArray(raw.items) || raw.items.length > 256) throw new Error('Invalid gallery page');
    const items = raw.items.map((value): GalleryItem => {
        const item = asRecord(value);
        return {
            msgId: integer(item.msg_id, 1), name: String(item.name ?? ''), size: integer(item.size),
            parentId: String(item.parent_id ?? ''), uploadTime: integer(item.upload_time), uploaderId: integer(item.uploader_id),
            encrypted: Boolean(item.encrypted), plaintextSize: integer(item.plaintext_size ?? 0),
            revision: integer(item.revision ?? 0), contentMsgId: integer(item.content_msg_id ?? item.msg_id), contentHash: String(item.content_hash ?? ''),
        };
    });
    return { generation: String(raw.generation ?? ''), startIndex: integer(raw.start_index), items, nextCursor: String(raw.next_cursor ?? '') };
}

/**
 * One tile's worth of folder metadata. Unlike a timeline, a folder row that
 * fails to make sense is dropped rather than thrown on: the rest of the grid
 * is still a working way to reach photos.
 */
export function normalizeMediaFolder(value: unknown): MediaFolder {
    const raw = asRecord(value);
    return {
        folderId: boundedText(raw.folder_id, MAX_FOLDER_ID),
        name: boundedText(raw.name, MAX_NAME),
        itemCount: integer(raw.item_count),
        latestUploadTime: integer(raw.latest_upload_time),
        coverMsgId: integer(raw.cover_msg_id),
        coverRevision: integer(raw.cover_revision),
        coverName: boundedText(raw.cover_name, MAX_NAME),
    };
}

export function normalizeMediaFolders(value: unknown): MediaFolder[] {
    if (!Array.isArray(value)) return [];
    const folders: MediaFolder[] = [];
    for (const entry of value) {
        // A row with a broken count or timestamp is one tile, not the grid.
        try { folders.push(normalizeMediaFolder(entry)); } catch { continue; }
    }
    return folders;
}

export async function getMediaTimeline(): Promise<MediaTimeline> {
    return normalizeMediaTimeline(await invokeBackend(GetMediaTimeline));
}
export async function getMediaTimelineSummary(): Promise<MediaTimeline> {
    return normalizeMediaTimeline(await invokeBackend(GetMediaTimelineSummary), true);
}
export async function getMediaTimelineAnchors(generation: string): Promise<MediaTimeline> {
    return normalizeMediaTimeline(await invokeBackend(GetMediaTimelineAnchors, generation));
}
export async function listMediaPage(cursor: string, limit = 128): Promise<MediaPage> {
    return normalizeMediaPage(await invokeBackend(ListMediaPage, cursor, limit));
}
/** The album grid. A drive with no media answers with null, not an error. */
export async function listMediaFolders(): Promise<MediaFolder[]> {
    return normalizeMediaFolders(await invokeBackend(ListMediaFolders));
}

/**
 * One folder's timeline. It carries month buckets but no anchor index: a
 * folder is small enough that its pages are reached by following cursors, so
 * missing anchors are the contract here rather than an incomplete answer.
 */
export async function getMediaFolderTimeline(folderId: string): Promise<MediaTimeline> {
    return normalizeMediaTimeline(await invokeBackend(GetMediaFolderTimeline, folderId), true);
}

export async function listMediaFolderPage(folderId: string, cursor: string, limit = 128): Promise<MediaPage> {
    return normalizeMediaPage(await invokeBackend(ListMediaFolderPage, folderId, cursor, limit));
}

export async function locateMedia(msgId: number, generation: string): Promise<MediaLocation> {
    const raw = asRecord(await invokeBackend(LocateMedia, msgId, generation));
    return { generation: String(raw.generation ?? ''), index: integer(raw.index), cursor: String(raw.cursor ?? '') };
}
