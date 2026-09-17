/**
 * The set of videos the player is currently moving through, and the panel that
 * shows it.
 *
 * The launch list is normalized and frozen on open so that later edits to the
 * gallery behind the player cannot renumber the queue mid-playback. The panel
 * itself is Svelte — this owns the queue, the open/closed state and the store
 * the component renders from.
 */

import { videoFormatLabel } from '../media-types';
import type { VideoChromeController } from './video-chrome';
import { byID } from './video-dom';
import { loadAutoNextPreference } from './video-playlist';
import {
    resetVideoPlaylist,
    setVideoPlaylist,
    setVideoPlaylistAutoNext,
    setVideoPlaylistCurrentIndex,
    setVideoPlaylistOpen,
    setVideoPlaylistSwitching,
    type VideoPlaylistViewItem,
} from '../../ui/video/video-playlist-store';

const PANEL_ID = 'video-playlist-panel';
const BUTTON_ID = 'video-playlist-button';
const DEFAULT_TITLE = 'Videos';

export interface VideoOpenTarget {
    id: number;
    name: string;
    key?: string;
    size?: number;
    encrypted?: boolean;
}

export interface VideoPlaylistLaunch {
    readonly items: readonly VideoOpenTarget[];
    readonly currentIndex: number;
    readonly title: string;
}

export interface ActiveVideoPlaylist {
    readonly items: readonly VideoOpenTarget[];
    readonly title: string;
    currentIndex: number;
    autoNext: boolean;
}

/** Rejects anything that cannot address a message, and fills in the rest. */
export function normalizeVideoTarget(target: VideoOpenTarget): VideoOpenTarget | null {
    const id = Number(target.id || 0);
    if (!Number.isFinite(id) || id <= 0) return null;
    const normalized = {
        id,
        name: String(target.name || 'Video'),
        size: Math.max(0, Number(target.size) || 0),
        encrypted: Boolean(target.encrypted),
    };
    const key = String(target.key || '').trim();
    return key ? { ...normalized, key } : normalized;
}

export interface VideoPlaylistContext {
    /** Resolved per call: the queue outlives any one activation of the modal. */
    modal(): HTMLElement | null;
    chrome: VideoChromeController;
    /** The settings dock shares the panel slot, so opening this closes that. */
    closeMenus(): void;
    syncPanelGeometry(): void;
    syncViewportInsets(): void;
    scheduleNativeResize(): void;
    applyPicture(): void;
    isOpen(): boolean;
}

export class VideoPlaylistSession {
    private active: ActiveVideoPlaylist | null = null;
    private panelOpen = false;
    private returnFocus: HTMLElement | null = null;

    constructor(private readonly ctx: VideoPlaylistContext) {}

    get isPanelOpen(): boolean {
        return this.panelOpen;
    }

    get playlist(): ActiveVideoPlaylist | null {
        return this.active;
    }

    get panel(): HTMLElement | null {
        return byID(PANEL_ID);
    }

    /** Whether auto-next should queue the following item up. */
    get autoNext(): boolean {
        return Boolean(this.active?.autoNext);
    }

    install(target: VideoOpenTarget, launch?: VideoPlaylistLaunch): void {
        this.active = createActivePlaylist(target, launch);
        this.syncSnapshot();
    }

    reset(): void {
        this.active = null;
        resetVideoPlaylist();
    }

    bindButton(): void {
        byID(BUTTON_ID)?.addEventListener('click', (event) => {
            event.stopPropagation();
            if (this.panelOpen) this.hidePanel(true);
            else this.showPanel();
        });
    }

    showPanel(): void {
        // One video is not a queue; the panel would be an empty gesture.
        if (!this.active || this.active.items.length < 2) return;
        this.ctx.closeMenus();
        const panel = this.panel;
        if (!panel) return;
        this.returnFocus = byID(BUTTON_ID);
        this.panelOpen = true;
        panel.hidden = false;
        panel.inert = false;
        panel.setAttribute('aria-hidden', 'false');
        this.ctx.modal()?.classList.add('has-video-playlist');
        byID(BUTTON_ID)?.setAttribute('aria-expanded', 'true');
        setVideoPlaylistOpen(true);
        this.ctx.syncPanelGeometry();
        this.ctx.chrome.clearTimer();
        this.ctx.chrome.reveal();
        // The panel animates in, so its final geometry — and the row to focus —
        // only exist on the next frame.
        requestAnimationFrame(() => {
            this.ctx.syncPanelGeometry();
            this.ctx.scheduleNativeResize();
            this.ctx.applyPicture();
            panel.querySelector<HTMLElement>("[aria-current='true']")?.focus({ preventScroll: true });
        });
    }

    hidePanel(restoreFocus = false): void {
        const panel = this.panel;
        const focusInside = Boolean(panel?.contains(document.activeElement));
        this.panelOpen = false;
        if (panel) {
            panel.hidden = true;
            panel.inert = true;
            panel.setAttribute('aria-hidden', 'true');
        }
        this.ctx.modal()?.classList.remove('has-video-playlist');
        byID(BUTTON_ID)?.setAttribute('aria-expanded', 'false');
        setVideoPlaylistOpen(false);
        // Focus must never be left inside an inert panel.
        if ((restoreFocus || focusInside) && this.returnFocus?.isConnected) {
            this.returnFocus.focus({ preventScroll: true });
        }
        this.ctx.syncViewportInsets();
        this.ctx.scheduleNativeResize();
        this.ctx.applyPicture();
    }

    /** The item auto-next would play, if auto-next is on and there is one. */
    nextTarget(): VideoOpenTarget | null {
        if (!this.active?.autoNext) return null;
        const next = this.active.items[this.active.currentIndex + 1];
        return next ? normalizeVideoTarget(next) : null;
    }

    itemAt(index: number): VideoOpenTarget | null {
        const items = this.active?.items;
        if (!items || index < 0 || index >= items.length) return null;
        return items[index];
    }

    currentIndex(): number {
        return this.active?.currentIndex ?? -1;
    }

    setCurrentIndex(index: number): void {
        if (!this.active) return;
        this.active.currentIndex = index;
        setVideoPlaylistCurrentIndex(index);
        this.syncButton();
    }

    setAutoNext(enabled: boolean): void {
        if (this.active) this.active.autoNext = enabled;
        setVideoPlaylistAutoNext(enabled);
    }

    markSwitching(id: string | null): void {
        setVideoPlaylistSwitching(id);
    }

    identity(item: VideoOpenTarget, index: number): string {
        return playlistItemIdentity(item, index);
    }

    syncSnapshot(): void {
        if (!this.active) {
            resetVideoPlaylist();
            this.syncButton();
            return;
        }
        setVideoPlaylist({
            open: this.panelOpen && this.active.items.length > 1,
            title: this.active.title,
            items: playlistViewItems(this.active),
            currentIndex: this.active.currentIndex,
            autoNext: this.active.autoNext,
            switchingId: null,
        });
        this.syncButton();
    }

    private syncButton(): void {
        const button = byID<HTMLButtonElement>(BUTTON_ID);
        if (!button) return;
        const count = this.active?.items.length ?? 0;
        const available = count > 1;
        button.hidden = !available;
        const position = available ? this.active!.currentIndex + 1 : 0;
        button.setAttribute('aria-label', available ? `Playlist, ${position} of ${count}` : 'Playlist');
        button.title = available ? `Playlist (${position} of ${count})` : 'Playlist';
    }
}

/** A stable per-row key; the message id alone repeats when a file is in the list twice. */
function playlistItemIdentity(item: VideoOpenTarget, index: number): string {
    return item.key || `video:${item.id}:${index}`;
}

function playlistViewItems(playlist: ActiveVideoPlaylist): readonly VideoPlaylistViewItem[] {
    return playlist.items.map((item, index) => ({
        id: playlistItemIdentity(item, index),
        name: item.name,
        size: item.size || 0,
        format: videoFormatLabel(item.name),
        position: index + 1,
    }));
}

/**
 * Builds the queue for one open. A launch whose index does not resolve to the
 * opened file unambiguously is discarded rather than guessed at: playing the
 * wrong neighbour next is worse than offering no queue at all.
 */
function createActivePlaylist(target: VideoOpenTarget, launch?: VideoPlaylistLaunch): ActiveVideoPlaylist {
    const fallback = Object.freeze([{ ...target }]);
    if (!launch) {
        return { items: fallback, title: DEFAULT_TITLE, currentIndex: 0, autoNext: loadAutoNextPreference() };
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
        return { items: fallback, title: DEFAULT_TITLE, currentIndex: 0, autoNext: loadAutoNextPreference() };
    }
    items[currentIndex] = { ...items[currentIndex], ...target };
    return {
        items: Object.freeze(items.map((item) => Object.freeze({ ...item }))),
        title: String(launch.title || DEFAULT_TITLE),
        currentIndex,
        autoNext: loadAutoNextPreference(),
    };
}
