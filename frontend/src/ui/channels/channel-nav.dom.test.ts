import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, tick, unmount } from 'svelte';
import ChannelNav from './ChannelNav.svelte';
import type { ChannelSource } from './channel-model';

const source = (channelId: number, title: string): ChannelSource => ({
    channelId, title, username: '', connected: true, protected: false, accountId: '7', generation: 'g',
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
        const loadPhoto = vi.fn(async (channel: ChannelSource) => (channel.channelId === 50 ? PHOTO : ''));
        host = document.createElement('div');
        document.body.append(host);
        app = mount(ChannelNav, {
            target: host,
            props: {
                sources: { status: 'ready', sources: [source(50, 'Field Recordings'), source(51, 'Cinema Club')] },
                activeId: 50, loadPhoto, onSelect: vi.fn(), onActions: vi.fn(), onAdd: vi.fn(), onRetry: vi.fn(),
            },
        });
        await settle();

        const [withPhoto, withoutPhoto] = Array.from(host.querySelectorAll('.channel-nav-item'));
        expect(withPhoto.querySelector('img')?.getAttribute('src')).toBe(PHOTO);
        expect(withoutPhoto.querySelector('img')).toBeNull();
        expect(withoutPhoto.textContent).toContain('C');
        expect(loadPhoto).toHaveBeenCalledTimes(2);
    });
});
