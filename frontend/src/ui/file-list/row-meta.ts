// Display helpers for the phone row: the two-line layout has room for one
// meta line, so size and age share it, and a long name keeps its extension by
// giving up its middle instead of its end.

import { formatBytes, formatDate } from '../../utils';
import type { FileListFileRow, FolderListRow } from './types';

const TAIL_CHARS = 4;

/** "Just now", "3 minutes ago", "Yesterday", then the calendar date past a week. */
export function relativeTimeLabel(unixSec: number, nowMs = Date.now()): string {
    if (!unixSec) return '';
    const diff = Math.max(0, Math.floor(nowMs / 1000) - unixSec);
    if (diff < 60) return 'Just now';
    const minutes = Math.floor(diff / 60);
    if (minutes < 60) return minutes === 1 ? '1 minute ago' : `${minutes} minutes ago`;
    const hours = Math.floor(minutes / 60);
    if (hours < 24) return hours === 1 ? '1 hour ago' : `${hours} hours ago`;
    const days = Math.floor(hours / 24);
    if (days === 1) return 'Yesterday';
    if (days < 7) return `${days} days ago`;
    return formatDate(unixSec);
}

/** "2 MB · 2 hours ago"; a folder whose stats are still unknown gets nothing. */
export function rowMetaLine(row: FolderListRow | FileListFileRow, nowMs = Date.now()): string {
    if (row.kind === 'folder') {
        if (row.size <= 0 && row.modifiedTime <= 0) return '';
        const parts = [row.size > 0 ? formatBytes(row.size) : '', relativeTimeLabel(row.modifiedTime, nowMs)];
        return parts.filter(Boolean).join(' · ');
    }
    return [formatBytes(row.size), relativeTimeLabel(row.uploadTime, nowMs)].filter(Boolean).join(' · ');
}

/**
 * Splits a name so the tail (the last few characters of the base plus the
 * extension) can stay while the head ellipsises. Names without an extension
 * keep the whole string in the head.
 */
export function splitRowLabel(name: string): { head: string; tail: string } {
    const dot = name.lastIndexOf('.');
    if (dot <= 0 || dot === name.length - 1) return { head: name, tail: '' };
    const cut = Math.max(0, dot - TAIL_CHARS);
    return { head: name.slice(0, cut), tail: name.slice(cut) };
}
