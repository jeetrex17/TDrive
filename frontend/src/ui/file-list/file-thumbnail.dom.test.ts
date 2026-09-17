import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import type { RenditionAsset } from '../../modules/renditions/broker';

const platform = vi.hoisted(() => ({
    mobile: false,
    listeners: new Map<string, (value: unknown) => void>(),
}));
const runtime = vi.hoisted(() => ({ acquire: vi.fn(), release: vi.fn(), reset: () => {} }));

vi.mock('../../api', () => ({
    isMobilePlatform: () => platform.mobile,
    onRuntimeEvent: (event: string, callback: (value: unknown) => void) => {
        platform.listeners.set(event, callback);
        return () => { platform.listeners.delete(event); };
    },
}));
vi.mock('../../modules/renditions/runtime', () => ({
    acquireRendition: runtime.acquire,
    subscribeRenditionReset: (callback: () => void) => {
        runtime.reset = callback;
        return () => {};
    },
}));

import FileList from './FileList.svelte';
import { state } from '../../state';
import { showFileListRows, showFileListState } from './file-list-store';
import type { FileListFileRow } from './types';

type ThumbnailRow = FileListFileRow & {
    thumbnail?: Readonly<{ channelId: number; fileId: number; revision: number }>;
};

let fire: (node: Element, visible?: boolean) => void;
let host: HTMLElement;
let app: Record<string, unknown> | null = null;

class TestObserver {
    constructor(callback: IntersectionObserverCallback) {
        fire = (node, visible = true) => callback([
            { target: node, isIntersecting: visible } as IntersectionObserverEntry,
        ], this as unknown as IntersectionObserver);
    }
    observe() {}
    unobserve() {}
    disconnect() {}
}

function row(overrides: Partial<ThumbnailRow> = {}): ThumbnailRow {
    return {
        kind: 'file', key: 'file:fs:42', selectionKey: 'file:42', id: '42',
        name: 'photo.jpg', baseName: 'photo', ext: 'JPG', source: 'fs', parentId: 'open-folder',
        channelId: 7, size: 128, metaLabel: 'Today', sizeLabel: '128 B', ariaLabel: 'File: photo.jpg',
        uploaderID: 1, uploadTime: 1, encrypted: false, canDelete: true, canRename: true, actions: [],
        ...overrides,
    };
}

async function flush(): Promise<void> {
    await Promise.resolve();
    await Promise.resolve();
}

beforeEach(() => {
    platform.mobile = false;
    platform.listeners.clear();
    state.activeChannel = { id: 7, title: 'Drive', kind: 'personal' };
    vi.stubGlobal('IntersectionObserver', TestObserver);
    runtime.release.mockReset();
    runtime.acquire.mockReset().mockImplementation(() => ({
        promise: Promise.resolve({ url: 'blob:thumbnail', width: 256, height: 256 }),
        release: runtime.release,
    }));
    host = document.createElement('div');
    host.id = 'file-list';
    document.body.append(host);
});

afterEach(async () => {
    if (app) await unmount(app);
    app = null;
    host.remove();
    state.activeChannel = null;
    showFileListState({ stateKind: 'loading', title: 'Loading files' });
    vi.unstubAllGlobals();
});

describe.each([
    ['desktop', false],
    ['mobile', true],
] as const)('visible file thumbnails on %s', (_name, mobile) => {
    it('loads only an explicitly eligible current-folder image and releases it offscreen', async () => {
        platform.mobile = mobile;
        showFileListRows([
            row({
                thumbnail: Object.freeze({ channelId: 7, fileId: 42, revision: 9 }),
            }),
            row({
                key: 'search:file:fs:43', selectionKey: 'file:43', id: '43', name: 'search.jpg',
                baseName: 'search',
            }),
            row({
                key: 'file:tg:44', selectionKey: 'file:44', id: '44', name: 'legacy.jpg',
                baseName: 'legacy', source: 'tg',
            }),
        ]);
        app = mount(FileList, { target: host });
        flushSync();

        const thumbnail = host.querySelector<HTMLElement>('.row-thumbnail');
        expect(thumbnail).not.toBeNull();
        expect(host.querySelectorAll('.row-thumbnail')).toHaveLength(1);
        expect(runtime.acquire).not.toHaveBeenCalled();

        fire(thumbnail!);
        await flush();

        expect(runtime.acquire).toHaveBeenCalledOnce();
        expect(runtime.acquire).toHaveBeenCalledWith({
            channelId: 7,
            fileId: 42,
            revision: 9,
            kind: 'thumbnail',
        }, 'visible');
        expect(thumbnail?.querySelector<HTMLImageElement>('.row-thumbnail-image')?.getAttribute('src')).toBe('blob:thumbnail');

        fire(thumbnail!, false);
        flushSync();
        expect(runtime.release).toHaveBeenCalledOnce();
        expect(thumbnail?.querySelector<HTMLImageElement>('.row-thumbnail-image')?.getAttribute('src')).toBeNull();
    });
});

it('does not paint a thumbnail that resolves after its row leaves the viewport', async () => {
    let resolve!: (asset: RenditionAsset) => void;
    runtime.acquire.mockImplementation(() => ({
        promise: new Promise<RenditionAsset>((done) => { resolve = done; }),
        release: runtime.release,
    }));
    showFileListRows([row({ thumbnail: { channelId: 7, fileId: 42, revision: 9 } })]);
    app = mount(FileList, { target: host });
    flushSync();
    const thumbnail = host.querySelector<HTMLElement>('.row-thumbnail')!;

    fire(thumbnail);
    fire(thumbnail, false);
    resolve({ url: 'blob:stale', width: 256, height: 256 });
    await flush();

    expect(runtime.release).toHaveBeenCalledOnce();
    expect(thumbnail.querySelector<HTMLImageElement>('.row-thumbnail-image')?.getAttribute('src')).toBeNull();
});

it('keeps the icon fallback and labels locked thumbnails without fetching originals', async () => {
    runtime.acquire.mockImplementationOnce(() => ({
        promise: Promise.reject({ code: 'encryption_password_required' }),
        release: runtime.release,
    }));
    showFileListRows([row({ thumbnail: { channelId: 7, fileId: 42, revision: 9 } })]);
    app = mount(FileList, { target: host });
    flushSync();
    const thumbnail = host.querySelector<HTMLElement>('.row-thumbnail')!;

    fire(thumbnail);
    await flush();

    expect(runtime.acquire).toHaveBeenCalledWith({
        channelId: 7,
        fileId: 42,
        revision: 9,
        kind: 'thumbnail',
    }, 'visible');
    expect(thumbnail.dataset.status).toBe('locked');
    expect(thumbnail.title).toBe('locked, click to unlock');
    expect(thumbnail.querySelector<HTMLImageElement>('.row-thumbnail-image')?.getAttribute('src')).toBeNull();
});

it('rearms a missing visible thumbnail when its derivative is published', async () => {
    runtime.acquire
        .mockImplementationOnce(() => ({
            promise: Promise.reject({ code: 'missing_rendition' }),
            release: runtime.release,
        }))
        .mockImplementationOnce(() => ({
            promise: Promise.resolve({ url: 'blob:ready', width: 256, height: 256 }),
            release: runtime.release,
        }));
    showFileListRows([row({ thumbnail: { channelId: 7, fileId: 42, revision: 9 } })]);
    app = mount(FileList, { target: host });
    flushSync();
    const thumbnail = host.querySelector<HTMLElement>('.row-thumbnail')!;

    fire(thumbnail);
    await flush();
    expect(thumbnail.dataset.status).toBe('missing');

    platform.listeners.get('gallery_rendition_ready')?.({ channel_id: 7, msg_id: 42, kind: 'thumbnail' });
    fire(thumbnail);
    await flush();

    expect(runtime.acquire).toHaveBeenCalledTimes(2);
    expect(thumbnail.querySelector<HTMLImageElement>('.row-thumbnail-image')?.getAttribute('src')).toBe('blob:ready');
});
