import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, tick, unmount } from 'svelte';
import ChannelSurface from './ChannelSurface.svelte';
import type { ChannelMediaItem, ChannelSource } from './channel-model';

const SOURCE: ChannelSource = { channelId: 51, title: 'Field Recordings', username: 'fieldrec', connected: true, protected: false, accountId: 'a', generation: 'g' };
const PROTECTED: ChannelSource = { ...SOURCE, channelId: 52, title: 'Members archive', protected: true };
const MEDIA: ChannelMediaItem = { msgId: 71, date: 1_735_689_600, name: 'forest-dawn.mp4', size: 2_048, mimeType: 'video/mp4', kind: 'video', caption: 'Dawn in the forest', streamable: true, blockReason: '', telegramUrl: 'https://t.me/example/71' };

let app: Record<string, unknown> | null = null;
let host: HTMLElement | null = null;

async function settle(): Promise<void> {
    for (let index = 0; index < 6; index += 1) {
        flushSync();
        await tick();
        await Promise.resolve();
    }
    flushSync();
}

function render(sources: ChannelSource[] = [SOURCE], items: ChannelMediaItem[] = [MEDIA]) {
    host = document.createElement('div');
    document.body.append(host);
    app = mount(ChannelSurface, {
        target: host,
        props: {
            loadConnected: vi.fn().mockResolvedValue(sources),
            loadCandidates: vi.fn().mockResolvedValue([]),
            connect: vi.fn(),
            disconnect: vi.fn().mockResolvedValue(undefined),
            fetchMedia: vi.fn().mockResolvedValue({ channelId: sources[0]?.channelId ?? 0, accountId: 'a', generation: 'g', items, nextOffsetId: 0, hasMore: false }),
            play: vi.fn(),
            openTelegram: vi.fn(),
        },
    });
}

afterEach(async () => {
    if (app) await unmount(app);
    host?.remove();
    app = null;
    host = null;
});

describe('ChannelSurface', () => {
    it('makes the source read-only and offers bounded media actions', async () => {
        render();
        await settle();
        expect(host?.textContent).toContain('Read-only source');
        expect(host?.querySelector('[aria-label="Play forest-dawn.mp4. Video, 2 KB, Jan 1, 2025"]')).not.toBeNull();
        expect(host?.querySelector('[aria-label="Open forest-dawn.mp4 in Telegram"]')).not.toBeNull();
        expect(host?.textContent).not.toContain('Download');
        expect(host?.textContent).not.toContain('Rename');
    });

    it('states that protected sources stay in Telegram', async () => {
        render([PROTECTED], [{ ...MEDIA, streamable: false, blockReason: 'Protected Telegram post' }]);
        await settle();
        expect(host?.textContent).toContain('Protected posts open in Telegram');
        expect(host?.textContent).toContain('Protected Telegram post');
        expect(host?.querySelector('[aria-label^="Play"]')).toBeNull();
        expect(host?.querySelector('[aria-label^="Open"]')).not.toBeNull();
    });
});
