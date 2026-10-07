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
    isPlayable,
    mediaMeta,
    mediaTitle,
    type ChannelMediaItem,
    type ChannelPageMemory,
    type ChannelSource,
    sourceKey,
} from '../ui/channels/channel-model';
import { showContextMenu, type ContextMenuItem } from '../ui/menus/context-menu-store';
import { splitNameAndExt } from '../utils';
import { exitPhotos } from './gallery';
import { notify, notifyAppError } from './notifications';
import { closeTrash } from './trash/controller';

let loadVersion = 0;
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
        channelSources.set({ status: 'ready', sources });
        const open = get(openChannelKey);
        if (open !== null && !sources.some((source) => sourceKey(source) === open)) closeChannel();
    } catch (error) {
        if (version !== loadVersion) return;
        channelSources.update((current) => ({ status: 'error', sources: current.sources, error }));
    }
}

/** Source state lives as long as the signed-in dashboard. */
export function activateChannelSources(): () => void {
    void loadChannelSources();
    return () => {
        loadVersion += 1;
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
    const added = await connectChannelSource(candidate);
    await loadChannelSources();
    showChannel(added);
}

/**
 * Removal only forgets this source locally; no Telegram content is changed.
 * The notification offers Undo because reconnecting is safe.
 */
export async function removeChannel(source: ChannelSource): Promise<void> {
    const wasOpen = get(openChannelKey) === sourceKey(source);
    try {
        await disconnectChannelSource(source);
    } catch (error) {
        notifyAppError(error, { title: `Could not remove ${source.title}` });
        return;
    }
    if (wasOpen) closeChannel();
    await loadChannelSources();
    notify({
        level: 'info',
        title: `Removed ${source.title}`,
        body: 'Nothing was removed from Telegram.',
        action: {
            label: 'Undo',
            run: async () => {
                try {
                    const restored = await connectChannelSource(source);
                    await loadChannelSources();
                    if (wasOpen) showChannel(restored);
                } catch (error) {
                    notifyAppError(error, { title: `Could not add ${source.title} back` });
                }
            },
        },
    });
}

export function openInTelegram(url: string): void {
    if (url) openExternalUrl(url);
}

export function showChannelActions(x: number, y: number, source: ChannelSource): void {
    const url = channelTelegramUrl(source);
    const items: ContextMenuItem[] = [
        ...(url ? [{ label: 'Open in Telegram', action: () => openInTelegram(url) }, { type: 'divider' as const }] : []),
        { label: 'Remove from TDrive', danger: true, action: () => removeChannel(source) },
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
        ...(isPlayable(item) ? [{ label: 'Play', icon: 'play' as const, primary: true, action: () => openChannelPost(item, source, posts) }] : []),
        ...(item.telegramUrl ? [{ label: 'Open in Telegram', icon: 'external' as const, action: () => openInTelegram(item.telegramUrl) }] : []),
    ];
    if (items.length === 0) return;
    showContextMenu(x, y, items, {
        header: { title: mediaTitle(item), meta: mediaMeta(item), ext: splitNameAndExt(item.name).ext },
    });
}

/**
 * Plays a post through the existing viewers with a capability scoped to this
 * channel. A video queues the other videos the list is showing, so the player's
 * playlist and auto-next work as they do in a folder. Restricted posts belong
 * to Telegram, so they open there instead.
 */
export async function openChannelPost(
    item: ChannelMediaItem,
    source: ChannelSource,
    posts: readonly ChannelMediaItem[] = [item],
): Promise<void> {
    if (!isPlayable(item)) {
        openInTelegram(item.telegramUrl);
        return;
    }
    const open = (post: ChannelMediaItem) => openChannelMedia(source, post.msgId);
    if (item.kind === 'audio') {
        const { activateFileViewerModal, openFileViewer } = await import('./modals/file-viewer');
        activateFileViewerModal();
        await openFileViewer({ id: item.msgId, name: item.name, size: item.size, encrypted: false }, () => open(item));
        return;
    }
    const { target, playlist } = channelVideoQueue(item, source, posts, open);
    const { activateVideoModal, openVideoModal } = await import('./modals/video');
    activateVideoModal();
    await openVideoModal(target, playlist);
}
