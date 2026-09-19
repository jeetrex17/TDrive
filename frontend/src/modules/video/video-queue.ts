/**
 * The queue model behind the player: what a playable target is, and how the
 * list the file view handed over becomes the list the playlist panel shows.
 *
 * This is the data half of the playlist, kept apart from the controller that
 * plays it. Everything here is pure -- given the same launch it produces the
 * same queue, opens nothing and touches no DOM -- which matters because the
 * one genuinely tricky decision in the playlist, working out which entry the
 * reader actually opened, is otherwise only reachable by opening a video.
 *
 * The controller owns playback and the current index over time; this module
 * owns the shape of the queue at the moment it is built.
 */

import { videoFormatLabel } from "../media-types";
import type { VideoPlaylistViewItem } from "../../ui/video/video-playlist-store";

/** A video the player can be pointed at. `key` is the file list's stable row key, when it has one. */
export interface VideoOpenTarget {
    id: number;
    name: string;
    key?: string;
    size?: number;
    encrypted?: boolean;
}

/** What a caller hands the player when opening one video out of a list. */
export interface VideoPlaylistLaunch {
    readonly items: readonly VideoOpenTarget[];
    readonly currentIndex: number;
    readonly title: string;
}

/** The queue the player is currently working through. Only the mutable fields change during playback. */
export interface ActiveVideoPlaylist {
    readonly items: readonly VideoOpenTarget[];
    readonly title: string;
    currentIndex: number;
    autoNext: boolean;
}

/**
 * Coerce a target from the outside world into one the player can trust, or
 * null when there is no usable file id behind it.
 *
 * Targets arrive from Svelte props and from bindings that have been through
 * JSON, so an id can turn up as a string or as 0, and a missing name would
 * otherwise reach the title bar as "undefined". Returning null rather than a
 * patched-up target is deliberate: an open with no id can only fail at the
 * backend, and failing here costs no Telegram round-trip.
 */
export function normalizeVideoTarget(target: VideoOpenTarget): VideoOpenTarget | null {
    const id = Number(target.id || 0);
    if (!Number.isFinite(id) || id <= 0) return null;
    const normalized = {
        id,
        name: String(target.name || "Video"),
        size: Math.max(0, Number(target.size) || 0),
        encrypted: Boolean(target.encrypted),
    };
    const key = String(target.key || "").trim();
    return key ? { ...normalized, key } : normalized;
}

/**
 * Build the queue for `target`, falling back to a queue of one.
 *
 * The launch's own currentIndex is only trusted when the entry it points at is
 * the file being opened. It comes from a list that may have been re-sorted or
 * filtered since, and a stale index would leave the panel highlighting one row
 * while a different video plays, and auto-next then continuing from the wrong
 * place. When it does not line up we look the target up by id instead, and
 * accept the result only if exactly one entry matches -- a duplicate id gives
 * no way to tell which row the reader clicked, and guessing would put auto-next
 * on the wrong branch of the list. Failing all that, the target plays alone,
 * which is worse than a playlist but is never the wrong playlist.
 */
export function createActivePlaylist(
    target: VideoOpenTarget,
    autoNext: boolean,
    launch?: VideoPlaylistLaunch,
): ActiveVideoPlaylist {
    const fallback = Object.freeze([{ ...target }]);
    if (!launch) {
        return { items: fallback, title: "Videos", currentIndex: 0, autoNext };
    }

    const items = launch.items
        .map(normalizeVideoTarget)
        .filter((item): item is VideoOpenTarget => item !== null);
    let currentIndex = Number.isInteger(launch.currentIndex) ? launch.currentIndex : -1;
    if (currentIndex < 0 || currentIndex >= items.length || items[currentIndex].id !== target.id) {
        const matches = items.reduce<number[]>((indices, item, index) => {
            if (item.id === target.id) indices.push(index);
            return indices;
        }, []);
        currentIndex = matches.length === 1 ? matches[0] : -1;
    }
    if (currentIndex < 0) {
        return { items: fallback, title: "Videos", currentIndex: 0, autoNext };
    }
    // The opened target is the better copy of its own entry: it carries whatever
    // the caller knew about the file, and the list row may predate that.
    items[currentIndex] = { ...items[currentIndex], ...target };
    return {
        items: Object.freeze(items.map((item) => Object.freeze({ ...item }))),
        title: String(launch.title || "Videos"),
        currentIndex,
        autoNext,
    };
}

/**
 * A key for a queue row that survives a re-render.
 *
 * The file list's row key is used when there is one; the index is folded into
 * the fallback because a queue may legitimately hold the same file id twice and
 * two rows sharing a key would make Svelte reuse one row's DOM for the other.
 */
export function playlistItemIdentity(item: VideoOpenTarget, index: number): string {
    return item.key || `video:${item.id}:${index}`;
}

/** The queue as the playlist panel renders it: labels and positions, no playback state. */
export function playlistViewItems(playlist: ActiveVideoPlaylist): readonly VideoPlaylistViewItem[] {
    return playlist.items.map((item, index) => ({
        id: playlistItemIdentity(item, index),
        name: item.name,
        size: item.size || 0,
        format: videoFormatLabel(item.name),
        position: index + 1,
    }));
}
