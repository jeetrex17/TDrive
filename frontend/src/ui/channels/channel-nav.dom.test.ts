import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, tick, unmount } from 'svelte';
import ChannelNav from './ChannelNav.svelte';
import type { ChannelSource } from './channel-model';

const source = (peerId: number, title: string): ChannelSource => ({
    peerKind: 'channel', peerId, title, username: '', connected: true, protected: false, available: true, accountId: '7', generation: 'g',
});
const PHOTO = 'data:image/jpeg;base64,/9j/4AAQ';

let app: Record<string, unknown> | null = null;
let host: HTMLElement | null = null;

async function settle(): Promise<void> {
    for (let index = 0; index < 4; index += 1) {
        flushSync();
        await tick();
        await Promise.resolve();
    }
    flushSync();
}

afterEach(async () => {
    vi.unstubAllGlobals();
    if (app) await unmount(app);
    host?.remove();
    app = null;
    host = null;
});

describe('ChannelNav', () => {
    it("shows a channel's own picture, and its initial when it has none", async () => {
        // Every avatar counts as on screen.
        vi.stubGlobal('IntersectionObserver', class {
            constructor(private readonly callback: IntersectionObserverCallback) {}
            observe(): void { this.callback([{ isIntersecting: true } as IntersectionObserverEntry], this as unknown as IntersectionObserver); }
            disconnect(): void {}
        });
        const loadPhoto = vi.fn(async (channel: ChannelSource) => (channel.peerId === 50 ? PHOTO : ''));
        host = document.createElement('div');
        document.body.append(host);
        app = mount(ChannelNav, {
            target: host,
            props: {
                sources: { status: 'ready', sources: [source(50, 'Field Recordings'), source(51, 'Cinema Club')] },
                activeKey: '7:channel:50:g', loadPhoto, onSelect: vi.fn(), onActions: vi.fn(), onAdd: vi.fn(), onRetry: vi.fn(),
            },
        });
        await settle();

        const [withPhoto, withoutPhoto] = Array.from(host.querySelectorAll('.channel-nav-item'));
        expect(withPhoto.querySelector('img')?.getAttribute('src')).toBe(PHOTO);
        expect(withoutPhoto.querySelector('img')).toBeNull();
        expect(withoutPhoto.textContent).toContain('C');
        expect(loadPhoto).toHaveBeenCalledTimes(2);
    });

    async function mountOne(onActions: () => void): Promise<void> {
        host = document.createElement('div');
        document.body.append(host);
        app = mount(ChannelNav, {
            target: host,
            props: {
                sources: { status: 'ready', sources: [source(50, 'Field Recordings')] },
                activeKey: '7:channel:50:g', loadPhoto: vi.fn(async () => ''), onSelect: vi.fn(), onActions,
                onAdd: vi.fn(), onRetry: vi.fn(),
            },
        });
        await settle();
    }

    it.each(['user', 'bot', 'group', 'supergroup', 'channel'] as const)('opens %s actions without selecting the source', async (peerKind) => {
        const onActions = vi.fn();
        const onSelect = vi.fn();
        host = document.createElement('div');
        document.body.append(host);
        app = mount(ChannelNav, {
            target: host,
            props: {
                sources: { status: 'ready', sources: [{ ...source(50, 'Field Recordings'), peerKind }] },
                activeKey: `7:${peerKind}:50:g`, loadPhoto: vi.fn(async () => ''), onSelect, onActions, onAdd: vi.fn(), onRetry: vi.fn(),
            },
        });
        await settle();

        host.querySelector<HTMLButtonElement>('.channel-nav-actions')?.click();
        expect(onActions).toHaveBeenCalledTimes(1);
        expect(onActions.mock.calls[0][2]).toMatchObject({ peerKind, peerId: 50, accountId: '7' });
        // Tapping the "…" must not also open the channel.
        expect(onSelect).not.toHaveBeenCalled();
    });

    it('keeps a chat and channel with the same peer ID distinct', async () => {
        const chat = { ...source(50, 'Mina'), peerKind: 'user' as const };
        const channel = source(50, 'Field Recordings');
        const onSelect = vi.fn();
        host = document.createElement('div');
        document.body.append(host);
        app = mount(ChannelNav, { target: host, props: {
            sources: { status: 'ready', sources: [chat, channel] },
            activeKey: '7:user:50:g', loadPhoto: vi.fn(async () => ''),
            onSelect, onActions: vi.fn(), onAdd: vi.fn(), onRetry: vi.fn(),
        } });
        await settle();
        const rows = host.querySelectorAll<HTMLButtonElement>('.channel-nav-item');
        expect(rows).toHaveLength(2);
        expect(rows[0].getAttribute('aria-current')).toBe('page');
        expect(rows[1].getAttribute('aria-current')).toBeNull();
        rows[1].click();
        expect(onSelect).toHaveBeenCalledWith(channel);
    });

    // Desktop shows no "…" on channel rows, so the keyboard reaches the same
    // actions through the Menu key and Shift+F10 on the focused row.
    it('opens actions from the keyboard on a focused row', async () => {
        const onActions = vi.fn();
        await mountOne(onActions);
        const row = host!.querySelector<HTMLButtonElement>('.channel-nav-item');

        row?.dispatchEvent(new KeyboardEvent('keydown', { key: 'ContextMenu', bubbles: true }));
        expect(onActions).toHaveBeenCalledTimes(1);
        row?.dispatchEvent(new KeyboardEvent('keydown', { key: 'F10', shiftKey: true, bubbles: true }));
        expect(onActions).toHaveBeenCalledTimes(2);
        // A plain key is left for the list to handle.
        row?.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
        expect(onActions).toHaveBeenCalledTimes(2);
    });
});
