import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, tick, unmount } from 'svelte';
import ChannelView from './ChannelView.svelte';
import type { ChannelTopicFetcher } from './channel-topics';
import type { ChannelMediaFetcher, ChannelMediaItem, ChannelMediaPage, ChannelPageMemory, ChannelSource } from './channel-model';

const SOURCE: ChannelSource = { peerKind: 'channel', peerId: 51, title: 'Field Recordings', username: 'fieldrec', connected: true, protected: false, available: true, accountId: '7', generation: 'g' };
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

function render(source: ChannelSource, fetchMedia: ChannelMediaFetcher, recentPages?: ChannelPageMemory, fetchTopics?: ChannelTopicFetcher, mobile = false) {
    const props = { source, fetchMedia, onOpenPost: vi.fn(), onOpenTelegram: vi.fn(), onPostActions: vi.fn(), onSavePosts: vi.fn(), recentPages, fetchTopics, mobile };
    host = document.createElement('div');
    document.body.append(host);
    app = mount(ChannelView, { target: host, props });
    return props;
}

function answering(...items: ChannelMediaItem[]): ChannelMediaFetcher {
    return vi.fn(async (request) => ({ peerKind: request.peerKind, peerId: request.peerId, accountId: '7', generation: 'g', items, nextOffsetId: 0, hasMore: false }));
}

function kindTrigger(): HTMLButtonElement | null | undefined {
    return host?.querySelector<HTMLButtonElement>('[aria-label^="File type"]');
}

// Desktop chooses a file type from the File type menu.
function chooseKind(label: string): void {
    kindTrigger()?.click();
    flushSync();
    Array.from(host?.querySelectorAll<HTMLButtonElement>('[role="menuitemradio"]') ?? []).find((option) => option.textContent?.trim() === label)?.click();
}

function rowNamed(name: RegExp): HTMLButtonElement | undefined {
    return Array.from(host?.querySelectorAll<HTMLButtonElement>('.channel-row-main') ?? [])
        .find((button) => name.test(button.getAttribute('aria-label') ?? ''));
}

afterEach(async () => {
    vi.unstubAllGlobals();
    if (app) await unmount(app);
    host?.remove();
    app = null;
    host = null;
});

describe('ChannelView', () => {
    it('offers phone actions for a copyable file with no viewer or Telegram link', async () => {
        const group = { ...SOURCE, peerKind: 'group' as const, username: '' };
        const archive = { ...MANUAL, name: 'archive.zip', telegramUrl: '', copyIdentity: 'attachment1' };
        const protectedArchive = { ...archive, msgId: 72, name: 'locked.zip', protected: true };
        const props = render(group, answering(archive, protectedArchive), undefined, undefined, true);
        await settle();
        const actions = host?.querySelector<HTMLButtonElement>('[aria-label="Actions for archive.zip"]');
        expect(actions).not.toBeNull();
        actions?.click();
        expect(props.onPostActions.mock.calls[0]?.slice(2, 4)).toEqual([archive, group]);
        expect(host?.querySelector('[aria-label="Actions for locked.zip"]')).toBeNull();
    });

    it('selects source files with plain, Shift, Command, and Ctrl clicks and saves eligible files only', async () => {
        const first = { ...VIDEO, copyIdentity: 'attachment1' };
        const second = { ...MANUAL, copyIdentity: 'attachment2' };
        const third = { ...VIDEO, msgId: 68, caption: 'After dark', copyIdentity: 'attachment3' };
        const props = render(SOURCE, answering(first, PAID, second, third));
        await settle();
        const button = (label: string) => Array.from(host?.querySelectorAll('button') ?? []).find((node) => node.textContent?.trim() === label);
        const click = async (name: RegExp, modifiers?: MouseEventInit) => {
            const row = rowNamed(name);
            expect(row).toBeDefined();
            row?.dispatchEvent(new MouseEvent('click', { bubbles: true, detail: 1, ...modifiers }));
            await settle();
        };

        await click(/Dawn in the forest/);
        expect(button('Save 1 file to My Drive')).toBeDefined();
        expect(button('Select files')).toBeUndefined();
        expect(props.onOpenPost).not.toHaveBeenCalled();
        expect(rowNamed(/Dawn in the forest/)?.getAttribute('aria-pressed')).toBe('true');
        await click(/After dark/, { shiftKey: true });
        expect(button('Save 3 files to My Drive')).toBeDefined();
        await click(/kit\.pdf/, { metaKey: true });
        expect(button('Save 2 files to My Drive')).toBeDefined();
        expect(rowNamed(/kit\.pdf/)?.getAttribute('aria-pressed')).toBe('false');
        await click(/kit\.pdf/, { ctrlKey: true });
        expect(button('Save 3 files to My Drive')).toBeDefined();
        await click(/After dark/, { ctrlKey: true });
        button('Save 2 files to My Drive')?.click();
        expect(props.onSavePosts).toHaveBeenCalledWith(SOURCE, [first, second], SOURCE.title);
    });

    it('selects and clears a range with the keyboard', async () => {
        const first = { ...VIDEO, copyIdentity: 'attachment1' };
        const second = { ...MANUAL, copyIdentity: 'attachment2' };
        render(SOURCE, answering(first, second));
        await settle();
        const firstRow = rowNamed(/Dawn in the forest/);
        const secondRow = rowNamed(/kit\.pdf/);
        firstRow?.focus();
        firstRow?.dispatchEvent(new KeyboardEvent('keydown', { bubbles: true, key: ' ' }));
        await settle();
        expect(firstRow?.getAttribute('aria-pressed')).toBe('true');
        firstRow?.dispatchEvent(new KeyboardEvent('keydown', { bubbles: true, key: 'ArrowDown', shiftKey: true }));
        await settle();
        expect(document.activeElement).toBe(secondRow);
        expect(secondRow?.getAttribute('aria-pressed')).toBe('true');
        secondRow?.dispatchEvent(new KeyboardEvent('keydown', { bubbles: true, key: 'Escape' }));
        await settle();
        expect(firstRow?.getAttribute('aria-pressed')).toBe('false');
        expect(secondRow?.getAttribute('aria-pressed')).toBe('false');
    });
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
        chooseKind('Videos');
        await settle();
        expect(kindTrigger()?.getAttribute('aria-label')).toBe('File type Videos');
        expect(fetchMedia).toHaveBeenLastCalledWith(expect.objectContaining({ peerKind: 'channel', peerId: 51, offsetId: 0, kind: 'video' }));
    });

    it('opens images and documents with Open labels and sends their filters', async () => {
        const image = { ...VIDEO, msgId: 80, kind: 'image', name: 'forest.jpg', caption: '' };
        const pdf = { ...VIDEO, msgId: 79, kind: 'pdf', name: 'guide.pdf', caption: '' };
        const fetchMedia = answering(image, pdf);
        const props = render(SOURCE, fetchMedia);
        await settle();
        rowNamed(/^Open forest\.jpg/)?.click();
        expect(props.onOpenPost).toHaveBeenCalledWith(image, SOURCE, [image, pdf]);
        expect(rowNamed(/^Open guide\.pdf/)).toBeDefined();
        for (const [label, kind] of [['Images', 'image'], ['Documents', 'document']]) {
            chooseKind(label);
            await settle();
            expect(fetchMedia).toHaveBeenLastCalledWith(expect.objectContaining({ offsetId: 0, kind }));
        }
    });

    it('reorders the rows and queues videos in the order chosen', async () => {
        const second = { ...VIDEO, msgId: 72, caption: 'Episode 2', date: VIDEO.date + 60 };
        const tenth = { ...VIDEO, msgId: 73, caption: 'Episode 10', date: VIDEO.date + 120 };
        const fetchMedia = vi.fn(async () => ({ peerKind: 'channel' as const, peerId: 51, accountId: '7', generation: 'g', items: [tenth, second], nextOffsetId: 9, hasMore: true }));
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

    it('opens on the rows from the last visit and swaps in the fresh page', async () => {
        let answer: (page: ChannelMediaPage) => void = () => {};
        const fetchMedia = vi.fn<ChannelMediaFetcher>(() => new Promise((resolve) => { answer = resolve; }));
        const remembered = new Map<number, readonly ChannelMediaItem[]>([[51, [VIDEO]]]);
        render(SOURCE, fetchMedia, {
            get: (source) => remembered.get(source.peerId),
            set: (source, items) => { remembered.set(source.peerId, items); },
        });
        await settle();
        expect(host?.querySelector('.channel-skeleton')).toBeNull();
        expect(rowNamed(/^Play Dawn/)).toBeDefined();

        const dusk = { ...VIDEO, msgId: 75, caption: 'Dusk on the river' };
        answer({ peerKind: 'channel', peerId: 51, accountId: '7', generation: 'g', items: [dusk, VIDEO], nextOffsetId: 0, hasMore: false });
        await settle();
        expect(rowNamed(/^Play Dusk/)).toBeDefined();
        expect(remembered.get(51)).toEqual([dusk, VIDEO]);

        // A filtered page is not what the next visit should open on.
        chooseKind('Videos');
        await settle();
        answer({ peerKind: 'channel', peerId: 51, accountId: '7', generation: 'g', items: [dusk], nextOffsetId: 0, hasMore: false });
        await settle();
        expect(remembered.get(51)).toEqual([dusk, VIDEO]);
    });

    it('stops loading older posts on its own once a page adds none', async () => {
        // The end of the list is always in view, as it is in a short list.
        vi.stubGlobal('IntersectionObserver', class {
            constructor(private readonly callback: IntersectionObserverCallback) {}
            observe(): void {
                queueMicrotask(() => this.callback([{ isIntersecting: true } as IntersectionObserverEntry], this as unknown as IntersectionObserver));
            }
            disconnect(): void {}
        });
        const fetchMedia = vi.fn<ChannelMediaFetcher>(async (request) => ({
            peerKind: 'channel', peerId: 51, accountId: '7', generation: 'g', items: request.offsetId === 0 ? [VIDEO] : [],
            nextOffsetId: request.offsetId === 0 ? 900 : request.offsetId - 100, hasMore: true,
        }));
        render(SOURCE, fetchMedia);
        await settle();
        await settle();
        expect(fetchMedia).toHaveBeenCalledTimes(2);

        Array.from(host?.querySelectorAll<HTMLButtonElement>('button') ?? []).find((button) => button.textContent === 'Look further back')?.click();
        await settle();
        expect(fetchMedia).toHaveBeenCalledTimes(3);
        expect(fetchMedia).toHaveBeenLastCalledWith(expect.objectContaining({ peerKind: 'channel', peerId: 51, offsetId: 800 }));
    });

    it('scopes files, playlist and search to the chosen topic and preserves unavailable selections', async () => {
        const forumSource = { ...SOURCE, peerKind: 'supergroup' as const, forum: true };
        const topics = [{ id: 1, title: 'Announcements', iconColor: 0, iconEmojiId: '', closed: false, hidden: false }, { id: 42, title: 'Movies', iconColor: 0, iconEmojiId: '', closed: true, hidden: false }];
        const fetchTopics: ChannelTopicFetcher = vi.fn(async () => ({ peerKind: forumSource.peerKind, peerId: 51, accountId: '7', generation: 'g', forum: true, topics, next: { offsetDate: 0, offsetId: 0, offsetTopic: 0 }, hasMore: false }));
        let missing = false;
        const movie = { ...VIDEO, topicId: 42 };
        const fetchMedia: ChannelMediaFetcher = vi.fn(async (request) => {
            if (missing && request.topicId) throw Object.assign(new Error('This topic is no longer available.'), { code: 'topic_unavailable' });
            return { peerKind: 'supergroup' as const, peerId: 51, accountId: '7', generation: 'g', forum: true, topicId: request.topicId, topics, items: [movie], nextOffsetId: 0, hasMore: false };
        });
        const props = render(forumSource, fetchMedia, undefined, fetchTopics);
        await settle();
        expect(host?.querySelector('.channel-row-topic')?.textContent).toContain('Movies');
        host?.querySelector<HTMLButtonElement>('[aria-label="Choose topic: All topics"]')?.click();
        await settle();
        const choose = (name: string) => Array.from(host?.querySelectorAll<HTMLButtonElement>('.topic-option') ?? []).find((button) => button.textContent?.trim() === name)?.click();
        expect(host?.textContent).toContain('Announcements');
        expect(host?.textContent).not.toContain('General');
        choose('Movies');
        await settle();
        expect(fetchMedia).toHaveBeenLastCalledWith(expect.objectContaining({ topicId: 42, offsetId: 0 }));
        expect(host?.querySelector('input[placeholder="Search files in Movies"]')).not.toBeNull();
        expect(host?.querySelector('.channel-row-topic')).toBeNull();
        rowNamed(/^Play Dawn/)?.click();
        expect(props.onOpenPost).toHaveBeenCalledWith(movie, expect.objectContaining({ title: 'Field Recordings · Movies' }), [movie]);
        missing = true;
        chooseKind('Videos');
        await settle();
        expect(host?.textContent).toContain('Topic unavailable');
        expect(host?.querySelector('[aria-label="Choose topic: Movies"]')).not.toBeNull();
        Array.from(host?.querySelectorAll<HTMLButtonElement>('button') ?? []).find((button) => button.textContent === 'View all topics')?.click();
        await settle();
        expect(fetchMedia).toHaveBeenLastCalledWith(expect.objectContaining({ topicId: 0 }));
        expect(rowNamed(/^Play Dawn/)).toBeDefined();
    });

    it('offers a retry when the first page fails', async () => {
        const fetchMedia = vi.fn<ChannelMediaFetcher>()
            .mockRejectedValueOnce(new Error('rpc error code 420: FLOOD_WAIT_30'))
            .mockResolvedValue({ peerKind: 'channel' as const, peerId: 51, accountId: '7', generation: 'g', items: [VIDEO], nextOffsetId: 0, hasMore: false });
        render(SOURCE, fetchMedia);
        await settle();
        expect(host?.textContent).toContain('Posts did not load');
        expect(host?.textContent).not.toContain('FLOOD_WAIT');
        Array.from(host?.querySelectorAll<HTMLButtonElement>('button') ?? []).find((button) => button.textContent === 'Try again')?.click();
        await settle();
        expect(rowNamed(/^Play Dawn/)).toBeDefined();
    });
});
