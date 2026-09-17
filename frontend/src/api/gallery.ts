import { GetMediaTimeline, GetMediaTimelineAnchors, GetMediaTimelineSummary, ListMediaPage, LocateMedia } from '../../bindings/TDrive/app';
import type { FileItem } from '../types';
import { invokeBackend } from './gateway';
import { asRecord } from './shared';

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
export async function locateMedia(msgId: number, generation: string): Promise<MediaLocation> {
    const raw = asRecord(await invokeBackend(LocateMedia, msgId, generation));
    return { generation: String(raw.generation ?? ''), index: integer(raw.index), cursor: String(raw.cursor ?? '') };
}
