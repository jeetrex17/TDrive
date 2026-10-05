import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, tick, unmount } from 'svelte';
import ChannelView from './ChannelView.svelte';
import type { ChannelMediaFetcher, ChannelMediaItem, ChannelSource } from './channel-model';

const SOURCE: ChannelSource = { channelId: 51, title: 'Field Recordings', username: 'fieldrec', connected: true, protected: false, accountId: '7', generation: 'g' };
const VIDEO: ChannelMediaItem = {
    msgId: 71, date: 1_735_689_600, name: 'forest-dawn.mp4', size: 2_048, duration: 0, mimeType: 'video/mp4', kind: 'video',
    caption: 'Dawn in the forest', streamable: true, blockReason: '', telegramUrl: 'https://t.me/fieldrec/71',
};
const PAID: ChannelMediaItem = { ...VIDEO, msgId: 70, caption: 'Members cut', streamable: false, blockReason: 'paid', telegramUrl: 'https://t.me/fieldrec/70' };
const MANUAL: ChannelMediaItem = { ...VIDEO, msgId: 69, kind: 'unknown', name: 'kit.pdf', caption: '', streamable: false, blockReason: 'unsupported_format' };

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

function render(source: ChannelSource, fetchMedia: ChannelMediaFetcher) {
    const props = { source, fetchMedia, onOpenPost: vi.fn(), onOpenTelegram: vi.fn(), onPostActions: vi.fn() };
    host = document.createElement('div');
    document.body.append(host);
    app = mount(ChannelView, { target: host, props });
    return props;
}

function answering(...items: ChannelMediaItem[]): ChannelMediaFetcher {
    return vi.fn(async (request) => ({ channelId: request.channelId, accountId: '7', generation: 'g', items, nextOffsetId: 0, hasMore: false }));
}

function rowNamed(name: RegExp): HTMLButtonElement | undefined {
    return Array.from(host?.querySelectorAll<HTMLButtonElement>('.channel-row-main') ?? [])
        .find((button) => name.test(button.getAttribute('aria-label') ?? ''));
}

afterEach(async () => {
    if (app) await unmount(app);
    host?.remove();
    app = null;
    host = null;
});

describe('ChannelView', () => {
    it('names restrictions in words and never shows the backend codes', async () => {
        render(SOURCE, answering(VIDEO, PAID, MANUAL));
        await settle();
        const text = host?.textContent ?? '';
        expect(text).toContain('Dawn in the forest');
        expect(text).toContain('Paid');
        expect(text).not.toMatch(/unsupported_format|\bpaid\b/);
        expect(rowNamed(/^Play Dawn in the forest/)).toBeDefined();
        expect(rowNamed(/^Open in Telegram: Members cut.*Paid$/)).toBeDefined();
        expect(rowNamed(/^Open in Telegram: kit\.pdf, PDF/)).toBeDefined();
    });

    it('hands a row to its owner and opens a playable post in Telegram from its own button', async () => {
        const props = render(SOURCE, answering(VIDEO));
        await settle();
        rowNamed(/^Play Dawn/)?.click();
        expect(props.onOpenPost).toHaveBeenCalledWith(VIDEO, SOURCE, [VIDEO]);
        host?.querySelector<HTMLButtonElement>('[aria-label="Open Dawn in the forest in Telegram"]')?.click();
        expect(props.onOpenTelegram).toHaveBeenCalledWith('https://t.me/fieldrec/71');
    });

    it('says a protected channel is protected once, not on every row', async () => {
        const locked = { ...VIDEO, streamable: false, blockReason: 'protected' };
        render({ ...SOURCE, protected: true }, answering(locked, { ...locked, msgId: 72 }));
        await settle();
        expect(host?.textContent?.match(/Protected/g)).toHaveLength(1);
        expect(host?.querySelector('.channel-row-badge')).toBeNull();
    });

    it('asks for the newest videos when Videos is chosen', async () => {
        const fetchMedia = answering(VIDEO);
        render(SOURCE, fetchMedia);
        await settle();
        const videos = Array.from(host?.querySelectorAll<HTMLButtonElement>('.channel-kinds button') ?? []).find((button) => button.textContent === 'Videos');
        videos?.click();
        await settle();
        expect(videos?.getAttribute('aria-pressed')).toBe('true');
        expect(fetchMedia).toHaveBeenLastCalledWith(expect.objectContaining({ channelId: 51, offsetId: 0, kind: 'video' }));
    });

    it('reorders the rows and queues videos in the order chosen', async () => {
        const second = { ...VIDEO, msgId: 72, caption: 'Episode 2', date: VIDEO.date + 60 };
        const tenth = { ...VIDEO, msgId: 73, caption: 'Episode 10', date: VIDEO.date + 120 };
        const fetchMedia = vi.fn(async () => ({ channelId: 51, accountId: '7', generation: 'g', items: [tenth, second], nextOffsetId: 9, hasMore: true }));
        const props = render(SOURCE, fetchMedia);
        await settle();
        const titles = () => Array.from(host?.querySelectorAll('.channel-row-title') ?? []).map((title) => title.textContent);
        expect(titles()).toEqual(['Episode 10', 'Episode 2']);

        host?.querySelector<HTMLButtonElement>('[aria-label="Sort by Newest"]')?.click();
        await settle();
        Array.from(host?.querySelectorAll<HTMLButtonElement>('[role="menuitemradio"]') ?? []).find((option) => option.textContent?.trim() === 'Name')?.click();
        await settle();

        expect(titles()).toEqual(['Episode 2', 'Episode 10']);
        expect(host?.textContent).toContain('Sorted among the posts loaded so far.');
        rowNamed(/^Play Episode 10/)?.click();
        expect(props.onOpenPost).toHaveBeenCalledWith(tenth, SOURCE, [second, tenth]);
    });

    it('offers a retry when the first page fails', async () => {
        const fetchMedia = vi.fn<ChannelMediaFetcher>()
            .mockRejectedValueOnce(new Error('rpc error code 420: FLOOD_WAIT_30'))
            .mockResolvedValue({ channelId: 51, accountId: '7', generation: 'g', items: [VIDEO], nextOffsetId: 0, hasMore: false });
        render(SOURCE, fetchMedia);
        await settle();
        expect(host?.textContent).toContain('Posts did not load');
        expect(host?.textContent).not.toContain('FLOOD_WAIT');
        Array.from(host?.querySelectorAll<HTMLButtonElement>('button') ?? []).find((button) => button.textContent === 'Try again')?.click();
        await settle();
        expect(rowNamed(/^Play Dawn/)).toBeDefined();
    });
});
