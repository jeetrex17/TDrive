import { get } from 'svelte/store';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, tick, unmount } from 'svelte';
import { AUTO_NEXT_STORAGE_KEY } from '../../modules/video/video-playlist';
import VideoPlaylistPanel from './VideoPlaylistPanel.svelte';
import {
    resetVideoPlaylist,
    setVideoPlaylist,
    setVideoPlaylistAutoNext,
    setVideoPlaylistOpen,
    videoPlaylistStore,
    type VideoPlaylistViewItem,
} from './video-playlist-store';

const items: readonly VideoPlaylistViewItem[] = [
    { id: 'intro', name: 'Introduction.mp4', size: 1024, format: 'MP4', position: 1 },
    { id: 'lesson', name: 'Lesson one.webm', size: 2048, format: 'WEBM', position: 2 },
    { id: 'outro', name: 'Outro.mkv', size: 4096, format: 'MKV', position: 3 },
];

let component: Record<string, unknown> | null = null;
let host: HTMLElement | null = null;
const storage = new Map<string, string>();
const localStorageShim: Storage = {
    get length(): number { return storage.size; },
    clear(): void { storage.clear(); },
    getItem(key: string): string | null { return storage.get(key) ?? null; },
    key(index: number): string | null { return Array.from(storage.keys())[index] ?? null; },
    removeItem(key: string): void { storage.delete(key); },
    setItem(key: string, value: string): void { storage.set(key, value); },
};

async function setup(props: Record<string, unknown> = {}): Promise<void> {
    Object.defineProperty(window, 'localStorage', { configurable: true, value: localStorageShim });
    setVideoPlaylist({ open: true, title: 'Course videos', items, currentIndex: 1, autoNext: true });
    host = document.createElement('div');
    document.body.appendChild(host);
    component = mount(VideoPlaylistPanel, { target: host, props });
    flushSync();
    await tick();
}

afterEach(async () => {
    if (component) await unmount(component);
    host?.remove();
    component = null;
    host = null;
    window.localStorage.removeItem(AUTO_NEXT_STORAGE_KEY);
    resetVideoPlaylist();
});

describe('VideoPlaylistPanel behavior', () => {
    it('renders the folder snapshot with metadata and one active row', async () => {
        await setup();

        expect(host?.querySelector('.video-playlist-title')?.textContent).toBe('Course videos');
        expect(host?.querySelector('.video-playlist-count')?.textContent?.trim()).toBe('3');
        expect(host?.querySelectorAll('.video-playlist-row')).toHaveLength(3);
        expect(host?.querySelector('.video-playlist-row[aria-current="true"]')?.getAttribute('data-playlist-index')).toBe('1');
        // Position shows as a standalone index column; the full "2 of 3" phrasing
        // stays in the accessible name rather than repeating in visible text.
        expect(host?.querySelector('[data-playlist-index="1"] .video-playlist-index')?.textContent?.trim()).toBe('2');
        expect(host?.querySelector('[data-playlist-index="1"]')?.getAttribute('aria-label')).toContain('2 of 3');
        expect(host?.querySelector('[data-playlist-index="1"]')?.textContent).toContain('WEBM');
        expect(host?.querySelector('[data-playlist-index="1"]')?.textContent).toContain('2 KB');
    });

    it('moves focus without wrapping and selects with Enter or Space', async () => {
        const onSelect = vi.fn();
        await setup({ onSelect });

        const first = host?.querySelector<HTMLElement>('[data-playlist-index="0"]');
        const last = host?.querySelector<HTMLElement>('[data-playlist-index="2"]');
        if (!first || !last) throw new Error('missing playlist rows');

        first.focus();
        first.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true }));
        await tick();
        await Promise.resolve();
        expect(document.activeElement).toBe(host?.querySelector('[data-playlist-index="1"]'));

        const second = host?.querySelector<HTMLElement>('[data-playlist-index="1"]');
        if (!second) throw new Error('missing middle playlist row');
        second.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
        expect(onSelect).toHaveBeenCalledWith(1);

        last.focus();
        last.dispatchEvent(new KeyboardEvent('keydown', { key: 'End', bubbles: true }));
        await tick();
        await Promise.resolve();
        expect(document.activeElement).toBe(last);
    });

    it('persists auto-next changes and closes on Escape', async () => {
        const onAutoNextChange = vi.fn((enabled: boolean) => setVideoPlaylistAutoNext(enabled));
        const onClose = vi.fn(() => setVideoPlaylistOpen(false));
        await setup({ onAutoNextChange, onClose });

        const input = host?.querySelector<HTMLInputElement>('.video-playlist-switch');
        if (!input) throw new Error('missing auto-next switch');
        input.checked = false;
        input.dispatchEvent(new Event('change', { bubbles: true }));
        flushSync();

        expect(onAutoNextChange).toHaveBeenCalledWith(false);
        expect(get(videoPlaylistStore).autoNext).toBe(false);
        expect(window.localStorage.getItem(AUTO_NEXT_STORAGE_KEY)).toBe('false');

        const row = host?.querySelector<HTMLElement>('.video-playlist-row');
        if (!row) throw new Error('missing playlist row');
        row.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
        expect(onClose).toHaveBeenCalledOnce();
        expect(get(videoPlaylistStore).open).toBe(false);
    });

    it('keeps the active row visible when the panel resizes', async () => {
        await setup();

        const list = host?.querySelector<HTMLElement>('.video-playlist-list');
        const active = host?.querySelector<HTMLElement>('[aria-current="true"]');
        if (!list || !active) throw new Error('missing active playlist row');
        vi.spyOn(list, 'getBoundingClientRect').mockReturnValue(new DOMRect(0, 100, 320, 100));
        vi.spyOn(active, 'getBoundingClientRect').mockReturnValue(new DOMRect(0, 230, 300, 66));

        window.dispatchEvent(new Event('resize'));

        expect(list.scrollTop).toBe(96);
    });
});
