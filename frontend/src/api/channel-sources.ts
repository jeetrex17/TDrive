import {
    ChannelSourcePhoto as rawPhoto,
    ConnectChannelSource as rawConnect,
    DisconnectChannelSource as rawDisconnect,
    ListChannelMedia as rawListMedia,
    ListChannelSourceCandidates as rawListCandidates,
    ListConnectedChannelSources as rawListConnected,
    ResolvePublicChannelSource as rawResolvePublic,
} from '../../bindings/TDrive/internal/app/driveservice';
import { OpenChannelMedia as rawOpenChannelMedia } from '../../bindings/TDrive/internal/app/mediaservice';
import type { MediaItem, MediaPage, SourceInfo } from '../../bindings/TDrive/backend/channelsource/models';
import { invokeBackend } from './gateway';
import { normalizeMediaOpenResult, type MediaOpenResult } from './media';
import type { ChannelMediaFetcher, ChannelMediaItem, ChannelMediaPage, ChannelSource, SourcePeerKind } from '../ui/channels/channel-model';

function source(value: SourceInfo): ChannelSource {
    return {
        peerKind: String(value.peer_kind || 'channel') as SourcePeerKind,
        peerId: Number(value.peer_id || value.channel_id || 0),
        title: String(value.title || 'Telegram source'),
        username: String(value.username || '').replace(/^@/, ''),
        connected: Boolean(value.connected),
        protected: Boolean(value.protected),
        available: value.available !== false,
        candidatesTruncated: Boolean(value.candidates_truncated),
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
        peerKind: String(value.peer_kind || 'channel') as SourcePeerKind,
        peerId: Number(value.peer_id || value.channel_id || 0),
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

export async function resolvePublicChannelSource(input: string): Promise<ChannelSource> {
    return source(await invokeBackend(rawResolvePublic, input));
}

export async function connectChannelSource(candidate: ChannelSource): Promise<ChannelSource> {
    return source(await invokeBackend(rawConnect, candidate.peerKind, candidate.peerId, candidate.publicUsername ?? '', Number(candidate.accountId)));
}

export async function disconnectChannelSource(sourceValue: ChannelSource): Promise<void> {
    await invokeBackend(rawDisconnect, sourceValue.peerKind, sourceValue.peerId, Number(sourceValue.accountId), sourceValue.generation);
}

/** A source profile photo as a data URL, or '' when it has none. */
export async function loadChannelSourcePhoto(sourceValue: ChannelSource): Promise<string> {
    const photo = String(await invokeBackend(rawPhoto, sourceValue.peerKind, sourceValue.peerId, Number(sourceValue.accountId), sourceValue.generation) ?? '');
    return photo ? `data:image/jpeg;base64,${photo}` : '';
}

export const listChannelMedia: ChannelMediaFetcher = async (request) => page(
    await invokeBackend(rawListMedia, request.peerKind, request.peerId, request.offsetId, request.limit, request.search, request.kind),
);

export async function openChannelMedia(sourceValue: ChannelSource, msgId: number): Promise<MediaOpenResult> {
    return normalizeMediaOpenResult(await invokeBackend(rawOpenChannelMedia, sourceValue.peerKind, sourceValue.peerId, msgId, Number(sourceValue.accountId), sourceValue.generation));
}
