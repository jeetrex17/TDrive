import {
    ChannelSourcePhoto as rawPhoto,
    ConnectChannelSource as rawConnect,
    DisconnectChannelSource as rawDisconnect,
    ListChannelMedia as rawListMedia,
    ListChannelSourceCandidates as rawListCandidates,
    ListConnectedChannelSources as rawListConnected,
} from '../../bindings/TDrive/internal/app/driveservice';
import { OpenChannelMedia as rawOpenChannelMedia } from '../../bindings/TDrive/internal/app/mediaservice';
import type { MediaItem, MediaPage, SourceInfo } from '../../bindings/TDrive/backend/channelsource/models';
import { invokeBackend } from './gateway';
import { normalizeMediaOpenResult, type MediaOpenResult } from './media';
import type { ChannelMediaFetcher, ChannelMediaItem, ChannelMediaPage, ChannelSource } from '../ui/channels/channel-model';

function source(value: SourceInfo): ChannelSource {
    return {
        channelId: Number(value.channel_id || 0),
        title: String(value.title || 'Telegram channel'),
        username: String(value.username || '').replace(/^@/, ''),
        connected: Boolean(value.connected),
        protected: Boolean(value.protected),
        accountId: String(value.account_id || ''),
        generation: String(value.generation || ''),
    };
}

function item(value: MediaItem): ChannelMediaItem {
    return {
        msgId: Number(value.msg_id || 0),
        date: Number(value.date || 0),
        name: String(value.name || ''),
        size: Math.max(0, Number(value.size || 0)),
        duration: Math.max(0, Number(value.duration || 0)),
        mimeType: String(value.mime_type || ''),
        kind: String(value.kind || ''),
        caption: String(value.caption || ''),
        streamable: Boolean(value.streamable),
        blockReason: String(value.block_reason || ''),
        telegramUrl: String(value.telegram_url || ''),
    };
}

function page(value: MediaPage): ChannelMediaPage {
    return {
        channelId: Number(value.channel_id || 0),
        accountId: String(value.account_id || ''),
        generation: String(value.generation || ''),
        items: (value.items ?? []).map(item),
        nextOffsetId: Math.max(0, Number(value.next_offset_id || 0)),
        hasMore: Boolean(value.has_more),
    };
}

export async function listConnectedChannelSources(): Promise<ChannelSource[]> {
    return (await invokeBackend(rawListConnected) ?? []).map(source);
}

export async function listChannelSourceCandidates(): Promise<ChannelSource[]> {
    return (await invokeBackend(rawListCandidates) ?? []).map(source);
}

export async function connectChannelSource(channelId: number, expectedAccountId: string): Promise<ChannelSource> {
    return source(await invokeBackend(rawConnect, channelId, Number(expectedAccountId)));
}

export async function disconnectChannelSource(channelId: number, expectedAccountId: string, expectedGeneration: string): Promise<void> {
    await invokeBackend(rawDisconnect, channelId, Number(expectedAccountId), expectedGeneration);
}

/** A channel's profile photo as a data URL, or '' when it has none. */
export async function loadChannelSourcePhoto(channelId: number): Promise<string> {
    const photo = String(await invokeBackend(rawPhoto, channelId) ?? '');
    return photo ? `data:image/jpeg;base64,${photo}` : '';
}

export const listChannelMedia: ChannelMediaFetcher = async (request) => page(
    await invokeBackend(rawListMedia, request.channelId, request.offsetId, request.limit, request.search, request.kind),
);

export async function openChannelMedia(channelId: number, msgId: number, accountId: string, generation: string): Promise<MediaOpenResult> {
    return normalizeMediaOpenResult(await invokeBackend(rawOpenChannelMedia, channelId, msgId, Number(accountId), generation));
}
