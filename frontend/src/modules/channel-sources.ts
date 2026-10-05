// Telegram channels the user has added to TDrive: the sidebar list, which one
// the main area shows, adding and removing them, and handing a post to the
// player. A channel is read-only and separate from drives; showing one never
// changes the active drive.

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
    openChannelId,
} from '../ui/channels/channel-store';
import {
    channelTelegramUrl,
    channelVideoQueue,
    isPlayable,
    mediaMeta,
    mediaTitle,
    type ChannelMediaItem,
    type ChannelSource,
} from '../ui/channels/channel-model';
import { showContextMenu, type ContextMenuItem } from '../ui/menus/context-menu-store';
import { splitNameAndExt } from '../utils';
import { exitPhotos } from './gallery';
import { notify, notifyAppError } from './notifications';
import { closeTrash } from './trash/controller';

let loadVersion = 0;
// One request per channel for the session. A failure is forgotten, so the
// next time the avatar is shown it tries again.
const photos = new Map<number, Promise<string>>();

/** Reloads the added channels. Only the newest call may publish its answer. */
export async function loadChannelSources(): Promise<void> {
    const version = ++loadVersion;
    channelSources.update((current) => ({ status: 'loading', sources: current.sources }));
    try {
        const sources = (await listConnectedChannelSources()).filter((source) => source.connected);
        if (version !== loadVersion) return;
        channelSources.set({ status: 'ready', sources });
        const open = get(openChannelId);
        if (open !== null && !sources.some((source) => source.channelId === open)) closeChannel();
    } catch (error) {
        if (version !== loadVersion) return;
        channelSources.update((current) => ({ status: 'error', sources: current.sources, error }));
    }
}

/** Lives as long as the dashboard: channels belong to the signed-in account. */
export function activateChannelSources(): () => void {
    void loadChannelSources();
    return () => {
        loadVersion += 1;
        photos.clear();
        closeChannel();
        channelPickerOpen.set(false);
        channelSources.set(EMPTY_CHANNEL_SOURCES);
    };
}

/** Shows a channel in the main area, leaving Photos or the trash first. */
export function showChannel(source: ChannelSource): void {
    if (state.virtualView === 'photos') exitPhotos();
    else if (state.virtualView === 'trash') closeTrash();
    openChannelId.set(source.channelId);
}

/** A channel's profile photo as a data URL, or '' to show its initial. */
export function channelPhoto(source: ChannelSource): Promise<string> {
    let photo = photos.get(source.channelId);
    if (!photo) {
        photo = loadChannelSourcePhoto(source.channelId).catch(() => {
            photos.delete(source.channelId);
            return '';
        });
        photos.set(source.channelId, photo);
    }
    return photo;
}

export function openChannelPicker(): void {
    channelPickerOpen.set(true);
}

export function closeChannelPicker(): void {
    channelPickerOpen.set(false);
}

/** Adds a joined channel and opens it, which is what the user picked it for. */
export async function addChannel(candidate: ChannelSource): Promise<void> {
    const added = await connectChannelSource(candidate.channelId, candidate.accountId);
    await loadChannelSources();
    showChannel(added);
}

/**
 * Removing only forgets the channel locally: the account stays joined and
 * nothing is deleted, so it is undone rather than confirmed.
 */
export async function removeChannel(source: ChannelSource): Promise<void> {
    const wasOpen = get(openChannelId) === source.channelId;
    try {
        await disconnectChannelSource(source.channelId, source.accountId, source.generation);
    } catch (error) {
        notifyAppError(error, { title: `Could not remove ${source.title}` });
        return;
    }
    if (wasOpen) closeChannel();
    await loadChannelSources();
    notify({
        level: 'info',
        title: `Removed ${source.title}`,
        body: "You're still subscribed in Telegram.",
        action: {
            label: 'Undo',
            run: async () => {
                try {
                    const restored = await connectChannelSource(source.channelId, source.accountId);
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

/** A post's own menu: right-click on desktop, a long press on a phone. */
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
    const open = (post: ChannelMediaItem) => openChannelMedia(source.channelId, post.msgId, source.accountId, source.generation);
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
