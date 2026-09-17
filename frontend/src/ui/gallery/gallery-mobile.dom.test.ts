import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import Gallery from './Gallery.svelte';
import { galleryView } from './gallery-store';
import { GallerySource } from './gallery-source';
import { setSelectedFileRowKeys } from '../file-list/row-state-store';
import type { FileItem } from '../../types';

vi.mock('../../api', () => ({
    getThumbnail: vi.fn(() => Promise.resolve('data:image/svg+xml;base64,')),
    isMobilePlatform: () => true,
    onRuntimeEvent: () => () => {},
}));
vi.mock('../../api/gallery', () => ({ listMediaPage: vi.fn() }));
vi.mock('../../modules/renditions/runtime', () => ({ acquireRendition: vi.fn(() => ({ promise: Promise.resolve({ url: 'blob:photo', width: 256, height: 256 }), release: vi.fn() })), subscribeRenditionReset: () => () => {} }));
vi.mock('../../modules/transfers', () => ({ chooseFilesForCurrentFolder: vi.fn() }));
vi.mock('../../modules/app-actions', () => ({ appActions: () => ({ refreshFiles: vi.fn() }) }));

let host: HTMLElement;
let app: Record<string, unknown> | null = null;

function makeItem(msgId: number): FileItem {
    return { msgId, name: `IMG_${msgId}.jpg`, size: 10, parentId: '', uploadTime: 0, uploaderId: 0, encrypted: false, plaintextSize: 0, revision: 1 };
}

beforeEach(() => {
    host = document.createElement('div');
    host.id = 'gallery-view';
    document.body.append(host);
});

afterEach(async () => {
    galleryView.set({ status: 'loading' });
    setSelectedFileRowKeys([]);
    flushSync();
    if (app) await unmount(app);
    app = null;
    host.remove();
});

describe('Gallery on a phone', () => {
    it('shows skeleton cells while loading and the phone copy when empty or failed', () => {
        app = mount(Gallery, { target: host });
        flushSync();
        expect(host.querySelectorAll('.gallery-skeleton-cell')).toHaveLength(12);

        galleryView.set({ status: 'empty' });
        flushSync();
        expect(host.textContent).toContain('No photos in this drive.');
        expect(host.querySelector('.gallery-empty-actions button')?.textContent).toBe('Upload photos');

        galleryView.set({ status: 'error' });
        flushSync();
        expect(host.querySelector('[role="alert"]')?.textContent).toContain('Could not load photos.');
        expect(host.querySelector('.gallery-empty-actions button')?.textContent).toBe('Retry');
    });

    it('marks cells with a check once anything is selected', async () => {
        const source = await makeSource(2);
        galleryView.set({ status: 'ready', source });
        app = mount(Gallery, { target: host });
        flushSync();
        expect(host.querySelector('.gallery-check')).toBeNull();

        setSelectedFileRowKeys(['file:2']);
        flushSync();
        const cells = host.querySelectorAll<HTMLElement>('.gallery-cell');
        expect(cells[0].querySelector('.gallery-check')).not.toBeNull();
        expect(cells[0].getAttribute('aria-pressed')).toBe('false');
        expect(cells[1].classList.contains('is-selected')).toBe(true);
        expect(cells[1].getAttribute('aria-pressed')).toBe('true');
    });
});

async function makeSource(count: number): Promise<GallerySource> {
    const source = new GallerySource({
        channelId: 1, generation: '1', totalCount: count, pageSize: 128,
        buckets: [{ key: '2026-07', startIndex: 0, count, uploadTime: 1 }],
        anchors: Array.from({ length: Math.ceil(count / 128) }, (_, page) => ({ startIndex: page * 128, cursor: String(page * 128) })),
    }, { load: async (cursor) => ({ generation: '1', startIndex: Number(cursor), nextCursor: '', items: Array.from({ length: Math.min(128, count - Number(cursor)) }, (_, offset) => ({ ...makeItem(Number(cursor) + offset + 1), revision: 1, contentMsgId: offset + 1, contentHash: '' })) }) });
    await source.get(0);
    return source;
}

describe('large library virtualization', () => {
    it('mounts a constant window and releases the top rows after a deep scrollbar jump', async () => {
        const source = await makeSource(100_000);
        galleryView.set({ status: 'ready', source });
        app = mount(Gallery, { target: host });
        flushSync();
        expect(host.querySelectorAll('.gallery-cell').length).toBeLessThan(60);
        expect(host.querySelectorAll('*').length).toBeLessThan(250);
        expect(host.querySelector('[data-id="1"]')).not.toBeNull();
        host.scrollTop = 500_000;
        host.dispatchEvent(new Event('scroll'));
        await new Promise((resolve) => requestAnimationFrame(resolve));
        await Promise.resolve();
        flushSync();
        expect(host.querySelector('[data-id="1"]')).toBeNull();
        expect(host.querySelectorAll('.gallery-cell').length).toBeLessThan(60);
        expect(host.querySelectorAll('*').length).toBeLessThan(250);
        source.dispose();
    });
});
