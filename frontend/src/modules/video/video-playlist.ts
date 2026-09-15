import { isVideoFile } from "../media-types";
import type { FileListFileRow } from "../../ui/file-list/types";

export type VideoPlaylistItem = FileListFileRow;

export interface VideoPlaylist {
    readonly items: readonly VideoPlaylistItem[];
    /** The item opened by the caller, or -1 when the target is no longer present. */
    readonly currentIndex: number;
}

export interface AutoNextStorage {
    getItem(key: string): string | null;
    setItem(key: string, value: string): void;
}

export const AUTO_NEXT_STORAGE_KEY = "tdrive.video.auto-next.v1";
export const DEFAULT_AUTO_NEXT = true;

function freezePlaylist(items: VideoPlaylistItem[], currentIndex: number): VideoPlaylist {
    return Object.freeze({
        items: Object.freeze(items),
        currentIndex,
    });
}

/**
 * Build the queue from the rows the file list already rendered. Filtering is
 * deliberately the only ordering operation: the visible file-list sort is the
 * playlist order.
 */
export function deriveVideoPlaylist(
    rows: readonly FileListFileRow[],
    targetId: string | number,
): VideoPlaylist {
    const items = rows.filter((row) => isVideoFile(row.name));
    return freezePlaylist(items, findVideoPlaylistIndex(items, targetId));
}

/** Match a stable row key, falling back only to an unambiguous file id. */
function findVideoPlaylistIndex(
    items: readonly VideoPlaylistItem[],
    targetId: string | number,
): number {
    const normalizedTargetId = String(targetId);
    if (!normalizedTargetId) return -1;

    const keyIndex = items.findIndex((item) => item.key === normalizedTargetId);
    if (keyIndex >= 0) return keyIndex;

    let idIndex = -1;
    for (let index = 0; index < items.length; index += 1) {
        if (items[index].id !== normalizedTargetId) continue;
        if (idIndex >= 0) return -1;
        idIndex = index;
    }
    return idIndex;
}

export function normalizeAutoNextPreference(value: unknown): boolean {
    if (typeof value === "boolean") return value;
    if (typeof value !== "string") return DEFAULT_AUTO_NEXT;

    const normalized = value.trim().toLowerCase();
    if (normalized === "true") return true;
    if (normalized === "false") return false;

    try {
        const parsed = JSON.parse(value);
        return typeof parsed === "boolean" ? parsed : DEFAULT_AUTO_NEXT;
    } catch {
        return DEFAULT_AUTO_NEXT;
    }
}

function isAutoNextStorage(value: unknown): value is AutoNextStorage {
    if (!value || typeof value !== "object") return false;
    const candidate = value as Partial<AutoNextStorage>;
    return typeof candidate.getItem === "function" && typeof candidate.setItem === "function";
}

function browserStorage(): AutoNextStorage | null {
    try {
        if (typeof window === "undefined") return null;
        const candidate = window.localStorage;
        return isAutoNextStorage(candidate) ? candidate : null;
    } catch {
        return null;
    }
}

function resolveStorage(storage: AutoNextStorage | null | undefined): AutoNextStorage | null {
    return storage === undefined ? browserStorage() : storage;
}

export function loadAutoNextPreference(storage?: AutoNextStorage | null): boolean {
    const source = resolveStorage(storage);
    if (!source) return DEFAULT_AUTO_NEXT;

    try {
        return normalizeAutoNextPreference(source.getItem(AUTO_NEXT_STORAGE_KEY));
    } catch {
        return DEFAULT_AUTO_NEXT;
    }
}

export function saveAutoNextPreference(
    enabled: boolean,
    storage?: AutoNextStorage | null,
): void {
    const source = resolveStorage(storage);
    if (!source) return;

    try {
        source.setItem(AUTO_NEXT_STORAGE_KEY, JSON.stringify(Boolean(enabled)));
    } catch {
        // Preferences must not make playback fail when browser storage is blocked.
    }
}
