import {
    ChannelSourcePhoto as rawPhoto,
    ConnectChannelSource as rawConnect,
    DisconnectChannelSource as rawDisconnect,
    ListChannelMedia as rawListMedia,
    ListChannelMediaByTopic as rawListTopicMedia,
    ListChannelSourceTopics as rawListTopics,
    ListChannelSourceCandidates as rawListCandidates,
    ListConnectedChannelSources as rawListConnected,
    ResolvePublicChannelSource as rawResolvePublic,
} from '../../bindings/TDrive/internal/app/driveservice';
import { OpenChannelMedia as rawOpenChannelMedia } from '../../bindings/TDrive/internal/app/mediaservice';
import type { MediaItem, MediaPage, SourceInfo } from '../../bindings/TDrive/backend/channelsource/models';
import { invokeBackend } from './gateway';
import { normalizeMediaOpenResult, type MediaOpenResult } from './media';
import type { ChannelTopic, ChannelTopicFetcher } from '../ui/channels/channel-topics';
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
        forum: Boolean(value.forum),
        candidatesTruncated: Boolean(value.candidates_truncated),
        accountId: String(value.account_id || ''),
        generation: String(value.generation || ''),
    };
}

function item(value: MediaItem): ChannelMediaItem {
    return {
        msgId: Number(value.msg_id || 0),
        topicId: Number(value.topic_id || 0),
        date: Number(value.date || 0),
        name: String(value.name || ''),
        size: Math.max(0, Number(value.size || 0)),
        duration: Math.max(0, Number(value.duration || 0)),
        mimeType: String(value.mime_type || ''),
        kind: String(value.kind || ''),
        caption: String(value.caption || ''),
        streamable: Boolean(value.streamable),
        protected: Boolean(value.protected),
        blockReason: String(value.block_reason || ''),
        telegramUrl: String(value.telegram_url || ''),
    };
}

function topic(value: { id: number; title: string; icon_color: number; icon_emoji_id: string; closed: boolean; hidden: boolean }): ChannelTopic {
    return {
        id: value.id,
        title: value.title,
        iconColor: value.icon_color,
        iconEmojiId: String(value.icon_emoji_id || ''),
        closed: value.closed,
        hidden: value.hidden,
    };
}

function page(value: MediaPage): ChannelMediaPage {
    if (value.error_code) {
        const message = value.error_code === 'topic_unavailable'
            ? 'This topic is no longer available.'
            : 'Topics are no longer enabled in this group.';
        throw Object.assign(new Error(message), { code: value.error_code });
    }
    return {
        peerKind: String(value.peer_kind || 'channel') as SourcePeerKind,
        peerId: Number(value.peer_id || value.channel_id || 0),
        accountId: String(value.account_id || ''),
        generation: String(value.generation || ''),
        forum: Boolean(value.forum),
        topicId: Number(value.topic_id || 0),
        topics: (value.topics ?? []).map(topic),
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
    (request.topicId ?? 0) > 0
        ? await invokeBackend(rawListTopicMedia, request.peerKind, request.peerId, request.topicId ?? 0, request.offsetId, request.limit, request.search, request.kind, Number(request.accountId), request.generation)
        : await invokeBackend(rawListMedia, request.peerKind, request.peerId, request.offsetId, request.limit, request.search, request.kind, Number(request.accountId), request.generation),
);

export async function openChannelMedia(sourceValue: ChannelSource, msgId: number): Promise<MediaOpenResult> {
    return normalizeMediaOpenResult(await invokeBackend(rawOpenChannelMedia, sourceValue.peerKind, sourceValue.peerId, msgId, Number(sourceValue.accountId), sourceValue.generation));
}

export const listChannelSourceTopics: ChannelTopicFetcher = async (request) => {
    const { source: target } = request;
    const value = await invokeBackend(rawListTopics, target.peerKind, target.peerId, request.offsetDate, request.offsetId, request.offsetTopic, request.limit, request.query, Number(target.accountId), target.generation);
    return {
        peerKind: value.peer_kind as SourcePeerKind,
        peerId: value.peer_id,
        accountId: String(value.account_id),
        generation: value.generation,
        forum: value.forum,
        topics: (value.topics ?? []).map(topic),
        hasMore: value.has_more,
        next: { offsetDate: value.next_offset_date, offsetId: value.next_offset_id, offsetTopic: value.next_offset_topic },
    };
};
