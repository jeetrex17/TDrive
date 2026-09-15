import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import Gallery from './Gallery.svelte';
import { galleryView } from './gallery-store';
import { setSelectedFileRowKeys } from '../file-list/row-state-store';
import type { FileItem } from '../../types';

vi.mock('../../api', () => ({
    getThumbnail: vi.fn(() => Promise.resolve('data:image/svg+xml;base64,')),
    isMobilePlatform: () => true,
}));
vi.mock('../../modules/transfers', () => ({ chooseFilesForCurrentFolder: vi.fn() }));
vi.mock('../../modules/app-actions', () => ({ appActions: () => ({ refreshFiles: vi.fn() }) }));

let host: HTMLElement;
let app: Record<string, unknown> | null = null;

function makeItem(msgId: number): FileItem {
    return { msgId, name: `IMG_${msgId}.jpg`, size: 10, parentId: '', uploadTime: 0, uploaderId: 0, encrypted: false, plaintextSize: 0 };
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

    it('marks cells with a check once anything is selected', () => {
        galleryView.set({ status: 'ready', groups: [{ label: 'July 2026', cells: [{ item: makeItem(1), index: 0 }, { item: makeItem(2), index: 1 }] }] });
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
