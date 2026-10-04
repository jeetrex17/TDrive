import { openChannelMedia, openExternalUrl } from '../../api';
import type { ChannelMediaItem, ChannelSource } from './channel-model';

/** Opens an explicit channel capability without reading or changing active drive. */
export async function playChannelMedia(item: ChannelMediaItem, source: ChannelSource): Promise<void> {
    if (!item.streamable || item.blockReason) return;
    if (item.kind === 'audio') {
        const { activateFileViewerModal, openFileViewer } = await import('../../modules/modals/file-viewer');
        activateFileViewerModal();
        await openFileViewer(
            { id: item.msgId, name: item.name || 'Telegram audio', size: item.size, encrypted: false },
            () => openChannelMedia(source.channelId, item.msgId, source.accountId, source.generation),
        );
        return;
    }
    const { activateVideoModal, openVideoModal } = await import('../../modules/modals/video');
    activateVideoModal();
    await openVideoModal(
        { id: item.msgId, name: item.name || 'Telegram media', size: item.size, encrypted: false },
        undefined,
        () => openChannelMedia(source.channelId, item.msgId, source.accountId, source.generation),
    );
}

export function openChannelTelegram(url: string): void {
    if (url) openExternalUrl(url);
}
