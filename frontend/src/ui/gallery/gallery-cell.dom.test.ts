// A locked cell has to say so where a phone can see it: the title attribute
// never shows on a touch screen, so the word goes on the cell and into the
// accessible name.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import GalleryCell from './GalleryCell.svelte';
import { PlaybackResume, playbackResumeKey } from '../../modules/video/playback-resume';
import type { CellPatch, CellRegistration } from './gallery-controller';
import type { FileItem } from '../../types';

const registry = vi.hoisted(() => ({ apply: null as ((patch: CellPatch) => void) | null }));

vi.mock('../../api', () => ({ getThumbnail: vi.fn(), isMobilePlatform: () => true }));
vi.mock('./gallery-controller', () => ({
    registerCell: (_node: HTMLElement, reg: CellRegistration) => { registry.apply = reg.apply; },
    unregisterCell: vi.fn(),
}));

let host: HTMLElement;
let app: Record<string, unknown> | null = null;

const item: FileItem = {
    msgId: 7, name: 'IMG_7.jpg', size: 10, parentId: '',
    uploadTime: 0, uploaderId: 0, encrypted: true, plaintextSize: 0,
    revision: 1,
};

function patch(next: CellPatch): void {
    registry.apply?.(next);
    flushSync();
}

beforeEach(() => {
    registry.apply = null;
    host = document.createElement('div');
    document.body.append(host);
    app = mount(GalleryCell, { target: host, props: { item, index: 0 } });
    flushSync();
});

afterEach(async () => {
    if (app) await unmount(app);
    app = null;
    host.remove();
    vi.unstubAllGlobals();
});

describe('a gallery cell on a phone', () => {
    it('names the locked state in the label and on the cell', () => {
        const cell = host.querySelector('.gallery-cell')!;
        expect(cell.getAttribute('aria-label')).toBe('IMG_7.jpg');
        expect(host.querySelector('.gallery-locked-pill')).toBeNull();

        patch({ status: 'locked', title: 'locked, tap to unlock' });
        expect(cell.classList.contains('is-locked')).toBe(true);
        expect(cell.getAttribute('aria-label')).toBe('IMG_7.jpg — locked, tap to unlock');
        expect(host.querySelector('.gallery-locked-pill')?.textContent).toBe('Locked');

        patch({ status: 'loaded', title: '', src: 'data:url:7' });
        expect(cell.getAttribute('aria-label')).toBe('IMG_7.jpg');
        expect(host.querySelector('.gallery-locked-pill')).toBeNull();
    });

    it('names a failed thumbnail too', () => {
        patch({ status: 'failed', title: "couldn't load" });
        expect(host.querySelector('.gallery-cell')!.getAttribute('aria-label')).toBe("IMG_7.jpg — couldn't load");
        // The word "Locked" belongs to the locked state alone.
        expect(host.querySelector('.gallery-locked-pill')).toBeNull();
    });
});


it('shows live watched progress only for the matching video and clears it on completion', async () => {
    await unmount(app!);
    const data = new Map<string, string>();
    vi.stubGlobal('localStorage', { getItem: (key: string) => data.get(key) ?? null, setItem: (key: string, value: string) => data.set(key, value) });
    const video = { ...item, name: 'lesson.mp4', encrypted: false };
    app = mount(GalleryCell, { target: host, props: { item: video, index: 0, accountId: 42, channelId: 11 } });
    flushSync();
    expect(host.querySelector('.gallery-watch-progress')).toBeNull();
    const recorder = new PlaybackResume(playbackResumeKey(42, { channelId: 11, fileId: 7, revision: 1 }));
    recorder.observe({ currentTime: 25, duration: 100, paused: false, loading: false });
    flushSync();
    expect(host.querySelector('.gallery-watch-progress-fill')?.getAttribute('style')).toContain('25%');
    expect(host.querySelector('.gallery-cell')?.getAttribute('aria-label')).toContain('25% watched');
    recorder.observe({ currentTime: 60, duration: 100, paused: true, loading: false });
    flushSync();
    expect(host.querySelector('.gallery-watch-progress-fill')?.getAttribute('style')).toContain('60%');
    recorder.complete();
    flushSync();
    expect(host.querySelector('.gallery-watch-progress')).toBeNull();
});

it('does not reuse progress across accounts, drives, revisions or image thumbnails', async () => {
    await unmount(app!);
    const data = new Map<string, string>();
    vi.stubGlobal('localStorage', { getItem: (key: string) => data.get(key) ?? null, setItem: (key: string, value: string) => data.set(key, value) });
    new PlaybackResume(playbackResumeKey(42, { channelId: 11, fileId: 7, revision: 1 }))
        .observe({ currentTime: 25, duration: 100, paused: false, loading: false });
    for (const props of [
        { item: { ...item, name: 'lesson.mp4' }, index: 0, accountId: 43, channelId: 11 },
        { item: { ...item, name: 'lesson.mp4' }, index: 0, accountId: 42, channelId: 12 },
        { item: { ...item, name: 'lesson.mp4', revision: 2 }, index: 0, accountId: 42, channelId: 11 },
        { item, index: 0, accountId: 42, channelId: 11 },
    ]) {
        app = mount(GalleryCell, { target: host, props });
        flushSync();
        expect(host.querySelector('.gallery-watch-progress')).toBeNull();
        await unmount(app);
        app = null;
    }
});
