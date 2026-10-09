// Telegram chats and channels added as read-only media sources. Browsing one
// never changes the active TDrive drive or imports its messages.

import { get } from 'svelte/store';
import {
    connectChannelSource,
    disconnectChannelSource,
    listConnectedChannelSources,
    loadChannelSourcePhoto,
    openChannelMedia,
    openExternalUrl,
} from '../api';
import { state } from '../state';
import {
    channelPickerOpen,
    channelSources,
    closeChannel,
    EMPTY_CHANNEL_SOURCES,
    openChannelKey,
} from '../ui/channels/channel-store';
import {
    channelTelegramUrl,
    channelVideoQueue,
    isOpenable,
    mediaMeta,
    mediaActionLabel,
    mediaTitle,
    type ChannelMediaItem,
    type ChannelPageMemory,
    type ChannelSource,
    sourceKey,
} from '../ui/channels/channel-model';
import { showContextMenu, type ContextMenuItem } from '../ui/menus/context-menu-store';
import { pushHistoryEvent } from './notif-bell';
import { splitNameAndExt } from '../utils';
import { exitPhotos } from './gallery';
import { dismissNotification, notify, notifyAppError } from './notifications';
import { closeTrash } from './trash/controller';

let loadVersion = 0;
let activationVersion = 0;
let sourcesActive = false;
let allConnectedSources: readonly ChannelSource[] = [];
interface PendingRemoval {
    source: ChannelSource;
    wasOpen: boolean;
    toastId: string;
    activation: number;
}
const pendingRemovals = new Map<string, PendingRemoval>();
const committingRemovals = new Map<string, Promise<void>>();

function peerKey(source: ChannelSource): string {
    return `${source.accountId}:${source.peerKind}:${source.peerId}`;
}

function visibleSources(): readonly ChannelSource[] {
    return allConnectedSources.filter((source) => {
        const key = peerKey(source);
        return !pendingRemovals.has(key) && !committingRemovals.has(key);
    });
}

function publishSources(): void {
    channelSources.update((current) => ({ ...current, sources: visibleSources() }));
}
// Profile photos are base64 data URLs, so their number must stay bounded even
// when a picker exposes a large Telegram dialog list. A failure is forgotten,
// letting a later visible avatar retry.
const photos = new Map<string, Promise<string>>();
const MAX_PHOTO_CACHE_ENTRIES = 64;
const MAX_RECENT_SOURCE_PAGES = 12;
const firstPages = new Map<string, readonly ChannelMediaItem[]>();

const pageKey = sourceKey;

/** Show a source's recent first page while its fresh page loads. */
export const recentChannelPages: ChannelPageMemory = {
    get: (source) => {
        const key = pageKey(source);
        const items = firstPages.get(key);
        if (items) {
            firstPages.delete(key);
            firstPages.set(key, items);
        }
        return items;
    },
    set: (source, items) => {
        const key = pageKey(source);
        firstPages.delete(key);
        firstPages.set(key, items);
        while (firstPages.size > MAX_RECENT_SOURCE_PAGES) {
            const oldest = firstPages.keys().next().value;
            if (oldest === undefined) break;
            firstPages.delete(oldest);
        }
    },
};

/** Reload connected sources. Only the newest call may publish its answer. */
export async function loadChannelSources(): Promise<void> {
    const version = ++loadVersion;
    channelSources.update((current) => ({ status: 'loading', sources: current.sources }));
    try {
        const sources = (await listConnectedChannelSources()).filter((source) => source.connected);
        if (version !== loadVersion) return;
        allConnectedSources = sources;
        channelSources.set({ status: 'ready', sources: visibleSources() });
        const open = get(openChannelKey);
        if (open !== null && !get(channelSources).sources.some((source) => sourceKey(source) === open)) closeChannel();
    } catch (error) {
        if (version !== loadVersion) return;
        channelSources.update((current) => ({ status: 'error', sources: current.sources, error }));
    }
}

/** Source state lives as long as the signed-in dashboard. */
export function activateChannelSources(): () => void {
    activationVersion += 1;
    sourcesActive = true;
    void loadChannelSources();
    return () => {
        for (const [key, pending] of pendingRemovals) {
            dismissNotification(pending.toastId);
            if (pendingRemovals.has(key)) finishRemoval(key);
        }
        activationVersion += 1;
        sourcesActive = false;
        loadVersion += 1;
        allConnectedSources = [];
        photos.clear();
        firstPages.clear();
        closeChannel();
        channelPickerOpen.set(false);
        channelSources.set(EMPTY_CHANNEL_SOURCES);
    };
}

/** Show a media source in the main area, leaving Photos or trash first. */
export function showChannel(source: ChannelSource): void {
    if (state.virtualView === 'photos') exitPhotos();
    else if (state.virtualView === 'trash') closeTrash();
    openChannelKey.set(sourceKey(source));
}

/** A source's profile photo as a data URL, or '' to show its initial. */
export function channelPhoto(source: ChannelSource): Promise<string> {
    const key = sourceKey(source);
    let photo = photos.get(key);
    if (photo) {
        photos.delete(key);
        photos.set(key, photo);
    }
    if (!photo) {
        photo = loadChannelSourcePhoto(source).catch(() => {
            photos.delete(key);
            return '';
        });
        while (photos.size >= MAX_PHOTO_CACHE_ENTRIES) {
            const oldest = photos.keys().next().value;
            if (oldest === undefined) break;
            photos.delete(oldest);
        }
        photos.set(key, photo);
    }
    return photo;
}

export function openChannelPicker(): void {
    channelPickerOpen.set(true);
}

export function closeChannelPicker(): void {
    channelPickerOpen.set(false);
}

/** Connect a selected source and open it. */
export async function addChannel(candidate: ChannelSource): Promise<void> {
    const key = peerKey(candidate);
    const pending = pendingRemovals.get(key);
    if (pending) {
        undoRemoval(key);
        showChannel(pending.source);
        return;
    }
    await committingRemovals.get(key);
    const added = await connectChannelSource(candidate);
    await loadChannelSources();
    showChannel(added);
}

/** Hide a source now and forget it locally only after its Undo toast is gone. */
export function removeChannel(source: ChannelSource): void {
    const key = peerKey(source);
    if (pendingRemovals.has(key) || committingRemovals.has(key)) return;
    if (!get(channelSources).sources.some((current) => sourceKey(current) === sourceKey(source))) return;
    const wasOpen = get(openChannelKey) === sourceKey(source);
    const pending: PendingRemoval = { source, wasOpen, toastId: '', activation: activationVersion };
    pendingRemovals.set(key, pending);
    if (wasOpen) closeChannel();
    publishSources();
    pending.toastId = notify({
        level: 'info',
        title: `Removed ${source.title}`,
        body: 'Nothing was removed from Telegram.',
        history: false,
        onRemoved: () => finishRemoval(key),
        action: {
            label: 'Undo',
            run: () => undoRemoval(key),
        },
    });
}

function undoRemoval(key: string): void {
    const pending = pendingRemovals.get(key);
    if (!pending) return;
    pendingRemovals.delete(key);
    dismissNotification(pending.toastId);
    publishSources();
    if (pending.wasOpen) showChannel(pending.source);
}

function finishRemoval(key: string): void {
    const pending = pendingRemovals.get(key);
    if (!pending) return;
    pendingRemovals.delete(key);
    const { source, activation } = pending;
    let failed = false;
    const work = disconnectChannelSource(source).then(() => {
        if (activation !== activationVersion) return;
        allConnectedSources = allConnectedSources.filter((current) => peerKey(current) !== key);
        pushHistoryEvent({ level: 'info', title: `Removed ${source.title}`, body: 'Nothing was removed from Telegram.', ts: Date.now() });
    }).catch((error: unknown) => {
        if (activation !== activationVersion) return;
        failed = true;
        notifyAppError(error, { title: `Could not remove ${source.title}` });
    }).finally(() => {
        committingRemovals.delete(key);
        if (activation !== activationVersion) {
            if (sourcesActive) void loadChannelSources();
            return;
        }
        loadVersion += 1;
        channelSources.set({ status: 'ready', sources: visibleSources() });
        if (failed) void loadChannelSources();
    });
    committingRemovals.set(key, work);
}

export function openInTelegram(url: string): void {
    if (url) openExternalUrl(url);
}

export function showChannelActions(x: number, y: number, source: ChannelSource): void {
    const url = channelTelegramUrl(source);
    const items: ContextMenuItem[] = [
        ...(url ? [{ label: 'Open in Telegram', icon: 'external' as const, action: () => openInTelegram(url) }, { type: 'divider' as const }] : []),
        { label: 'Remove from TDrive', icon: 'clear', danger: true, action: () => removeChannel(source) },
    ];
    showContextMenu(x, y, items);
}

/** A media item's menu: right-click on desktop, long-press on a phone. */
export function showPostActions(
    x: number,
    y: number,
    item: ChannelMediaItem,
    source: ChannelSource,
    posts: readonly ChannelMediaItem[],
): void {
    const items: ContextMenuItem[] = [
        ...(isOpenable(item) ? [{ label: mediaActionLabel(item), icon: (mediaActionLabel(item) === 'Play' ? 'play' : 'open') as 'play' | 'open', primary: true, action: () => openChannelPost(item, source, posts) }] : []),
        ...(item.telegramUrl ? [{ label: 'Open in Telegram', icon: 'external' as const, action: () => openInTelegram(item.telegramUrl) }] : []),
    ];
    if (items.length === 0) return;
    showContextMenu(x, y, items, {
        header: { title: mediaTitle(item), meta: mediaMeta(item), ext: splitNameAndExt(item.name).ext },
    });
}

/**
 * Opens a post through the existing viewers with a capability scoped to this
 * channel. A video queues the other videos the list is showing, so the player's
 * playlist and auto-next work as they do in a folder. Unsupported, paid and expiring posts open in Telegram. Protected posts
 * remain view-only in TDrive.
 */
export async function openChannelPost(
    item: ChannelMediaItem,
    source: ChannelSource,
    posts: readonly ChannelMediaItem[] = [item],
): Promise<void> {
    if (!isOpenable(item)) {
        openInTelegram(item.telegramUrl);
        return;
    }
    const open = (post: ChannelMediaItem) => openChannelMedia(source, post.msgId);
    if (item.kind === 'audio' || item.kind === 'image' || item.kind === 'pdf' || item.kind === 'text') {
        const { activateFileViewerModal, openFileViewer } = await import('./modals/file-viewer');
        activateFileViewerModal();
        await openFileViewer({ id: item.msgId, name: item.name, size: item.size, kind: item.kind, protected: Boolean(source.protected || item.protected), encrypted: false }, () => open(item));
        return;
    }
    const { target, playlist } = channelVideoQueue(item, source, posts, open);
    const { activateVideoModal, openVideoModal } = await import('./modals/video');
    activateVideoModal();
    await openVideoModal(target, playlist);
}
