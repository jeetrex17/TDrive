import { writable, type Writable } from 'svelte/store';
import {
    loadAutoNextPreference,
    saveAutoNextPreference,
} from '../../modules/video/video-playlist';

/**
 * The deliberately small UI-facing shape of a playlist row.
 *
 * The player queue can keep its richer file-list rows, but the panel only
 * needs this immutable display snapshot. Positions are one-based for speech
 * and display ("2 of 7").
 */
export interface VideoPlaylistViewItem {
    readonly id: string;
    readonly name: string;
    readonly size: number;
    readonly format: string;
    readonly position: number;
}

export interface VideoPlaylistState {
    readonly open: boolean;
    readonly title: string;
    readonly items: readonly VideoPlaylistViewItem[];
    readonly currentIndex: number;
    readonly autoNext: boolean;
    readonly switchingId: string | null;
}

/** A partial state update used when a new folder snapshot is installed. */
export interface VideoPlaylistSnapshot {
    readonly open?: boolean;
    readonly title?: string;
    readonly items: readonly VideoPlaylistViewItem[];
    readonly currentIndex?: number;
    readonly autoNext?: boolean;
    readonly switchingId?: string | null;
}

const DEFAULT_TITLE = 'Videos';

function normalizeItem(item: VideoPlaylistViewItem, fallbackPosition: number): VideoPlaylistViewItem {
    const size = Number(item.size);
    const position = Number(item.position);

    return Object.freeze({
        id: item.id === null || item.id === undefined ? '' : String(item.id),
        name: String(item.name || '').trim() || 'Untitled video',
        size: Number.isFinite(size) ? Math.max(0, Math.trunc(size)) : 0,
        format: String(item.format || '').trim() || 'VIDEO',
        position: Number.isFinite(position) && position > 0
            ? Math.trunc(position)
            : fallbackPosition,
    });
}

function snapshotItems(items: readonly VideoPlaylistViewItem[]): readonly VideoPlaylistViewItem[] {
    return Object.freeze(items.map((item, index) => normalizeItem(item, index + 1)));
}

function normalizeIndex(index: number, itemCount: number): number {
    if (itemCount === 0) return -1;
    if (!Number.isFinite(index)) return -1;
    return Math.max(-1, Math.min(itemCount - 1, Math.trunc(index)));
}

function normalizeSwitchingId(id: string | null | undefined): string | null {
    return id === null || id === undefined || id === '' ? null : String(id);
}

function initialState(): VideoPlaylistState {
    return {
        open: false,
        title: DEFAULT_TITLE,
        items: Object.freeze([]),
        currentIndex: -1,
        autoNext: loadAutoNextPreference(),
        switchingId: null,
    };
}

/** The single source of truth for the playlist panel and imperative player bridge. */
export const videoPlaylistStore: Writable<VideoPlaylistState> = writable(initialState());

/**
 * Install a complete folder snapshot. Items are copied and frozen so later
 * file-list refreshes cannot mutate the queue while a video is playing.
 */
export function setVideoPlaylist(snapshot: VideoPlaylistSnapshot): void {
    const items = snapshotItems(snapshot.items);

    videoPlaylistStore.update((previous) => {
        const autoNext = snapshot.autoNext === undefined
            ? previous.autoNext
            : Boolean(snapshot.autoNext);

        return {
            open: snapshot.open ?? false,
            title: String(snapshot.title || '').trim() || DEFAULT_TITLE,
            items,
            currentIndex: normalizeIndex(snapshot.currentIndex ?? -1, items.length),
            autoNext,
            switchingId: normalizeSwitchingId(snapshot.switchingId),
        };
    });
}

/** Return the panel to its closed, empty state while retaining the saved preference. */
export function resetVideoPlaylist(): void {
    const autoNext = loadAutoNextPreference();
    videoPlaylistStore.set({
        open: false,
        title: DEFAULT_TITLE,
        items: Object.freeze([]),
        currentIndex: -1,
        autoNext,
        switchingId: null,
    });
}

export function setVideoPlaylistOpen(open: boolean): void {
    videoPlaylistStore.update((state) => ({ ...state, open: Boolean(open) }));
}

export function setVideoPlaylistCurrentIndex(index: number): void {
    videoPlaylistStore.update((state) => ({
        ...state,
        currentIndex: normalizeIndex(index, state.items.length),
    }));
}

export function setVideoPlaylistSwitching(switchingId: string | null): void {
    videoPlaylistStore.update((state) => ({
        ...state,
        switchingId: normalizeSwitchingId(switchingId),
    }));
}

export function setVideoPlaylistAutoNext(enabled: boolean): void {
    const autoNext = Boolean(enabled);
    videoPlaylistStore.update((state) => ({ ...state, autoNext }));
    saveAutoNextPreference(autoNext);
}
