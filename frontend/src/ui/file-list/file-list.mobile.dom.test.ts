// The phone row's own contract: the transfer badge has to land on the file the
// transfer is for, and the card's rounded ends have to be the list's ends and
// not the virtualiser's.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';

const api = vi.hoisted(() => ({
    isMobilePlatform: vi.fn(() => true),
    isIOSPlatform: () => false,
    isAndroidPlatform: () => false,
}));
vi.mock('../../api', () => api);

import FileList from './FileList.svelte';
import { showFileListRows, showFileListState } from './file-list-store';
import { resetFileSortState } from './file-sort-store';
import { setActiveFileRowKey, setSelectedFileRowKeys } from './row-state-store';
import { historyEvents, type TransferEvent } from '../notifications/notif-store';
import { state } from '../../state';
import type { FileListFileRow } from './types';

let app: Record<string, unknown> | null = null;
let list: HTMLElement | null = null;

function makeFileRow(overrides: Partial<FileListFileRow> = {}): FileListFileRow {
    return {
        kind: 'file',
        key: 'file:fs:42',
        selectionKey: 'file:42',
        id: '42',
        name: 'clip.mp4',
        baseName: 'clip',
        ext: 'MP4',
        source: 'fs',
        parentId: '',
        size: 128,
        metaLabel: 'Today',
        sizeLabel: '128 B',
        ariaLabel: 'File: clip.mp4',
        uploaderID: 0,
        uploadTime: 0,
        encrypted: false,
        canDelete: true,
        canRename: true,
        actions: [],
        ...overrides,
    };
}

function transfer(id: string, overrides: Partial<TransferEvent> = {}): TransferEvent {
    return {
        kind: 'transfer',
        id,
        direction: 'down',
        name: 'clip.mp4',
        progress: 10,
        total: 0,
        bytes: 0,
        speed: 0,
        status: 'active',
        startedAt: 0,
        finishedAt: 0,
        ...overrides,
    };
}

function longList(count: number): FileListFileRow[] {
    return Array.from({ length: count }, (_, index) => makeFileRow({
        id: String(index),
        key: `file:fs:${index}`,
        selectionKey: `file:${index}`,
        name: `entry-${index}.txt`,
    }));
}

function setup({ desktopGrid = false }: { desktopGrid?: boolean } = {}): void {
    list = document.createElement('div');
    list.id = 'file-list';
    if (desktopGrid) {
        list.setAttribute('role', 'grid');
        list.setAttribute('aria-colcount', '4');
        list.setAttribute('aria-rowcount', '99');
        list.setAttribute('aria-multiselectable', 'true');
    }
    document.body.appendChild(list);
    app = mount(FileList, { target: list, props: {} });
    flushSync();
}

function rows(): HTMLElement[] {
    return Array.from(list?.querySelectorAll<HTMLElement>('.drive-row') ?? []);
}

beforeEach(() => {
    historyEvents.set([]);
    state.activeChannel = { id: 1, title: 'Drive A', kind: 'personal' };
});

afterEach(async () => {
    historyEvents.set([]);
    resetFileSortState();
    showFileListState({ stateKind: 'loading', title: 'Loading files' });
    setSelectedFileRowKeys([]);
    setActiveFileRowKey('');
    flushSync();
    if (app) await unmount(app);
    list?.remove();
    app = null;
    list = null;
});

describe('phone row transfer badge', () => {
    it('uses a list rather than leaking the desktop data-grid model to assistive technology', () => {
        setup({ desktopGrid: true });
        // renderFileListRows writes the desktop row count immediately before it
        // publishes rows; the mobile subscriber must strip it again.
        list?.setAttribute('aria-rowcount', '1');
        showFileListRows([makeFileRow()]);
        setSelectedFileRowKeys(['file:42']);
        flushSync();

        const item = rows()[0];
        expect(list?.getAttribute('role')).toBe('list');
        expect(list?.hasAttribute('aria-colcount')).toBe(false);
        expect(list?.hasAttribute('aria-rowcount')).toBe(false);
        expect(list?.hasAttribute('aria-multiselectable')).toBe(false);
        expect(item.getAttribute('role')).toBe('listitem');
        expect(item.hasAttribute('aria-rowindex')).toBe(false);
        expect(item.hasAttribute('aria-selected')).toBe(false);
        expect(item.getAttribute('aria-posinset')).toBe('1');
        expect(item.getAttribute('aria-setsize')).toBe('1');
        expect(item.querySelector('[role="gridcell"]')).toBeNull();
    });

    it('badges the row a download is actually for', () => {
        // The download queue keys its jobs "file:<id>"; a row that only matched
        // a bare id meant no download ever badged anything.
        setup();
        showFileListRows([makeFileRow()]);
        historyEvents.set([transfer('xfer:down:file:42')]);
        flushSync();

        expect(rows()[0].querySelector('.item-status')?.getAttribute('aria-label'))
            .toBe('Downloading. Open transfers.');
    });

    it('explains a failed download on the row itself', () => {
        setup();
        showFileListRows([makeFileRow()]);
        historyEvents.set([transfer('xfer:down:file:42', { status: 'failed' })]);
        flushSync();

        expect(rows()[0].classList.contains('needs-explanation')).toBe(true);
        expect(rows()[0].querySelector('.row-explain')?.textContent)
            .toBe('The last transfer did not finish.');
    });

    it('keeps a drive-scoped failed transfer in the explanatory virtual-row set', () => {
        setup();
        showFileListRows([makeFileRow()]);
        historyEvents.set([transfer('xfer:down:file:1:42', { status: 'failed' })]);
        flushSync();

        // The visible row proves the drive-scoped key joined by its file id;
        // the same join feeds the virtualiser's tall-row calculation.
        expect(rows()[0].classList.contains('needs-explanation')).toBe(true);
    });

    it('leaves rows alone for a transfer that names no file', () => {
        // "xfer:up:42" is the 43rd file of an upload batch, not the file with
        // id 42 -- it used to mark this unrelated row failed.
        setup();
        showFileListRows([makeFileRow()]);
        historyEvents.set([transfer('xfer:up:42', { direction: 'up', status: 'failed' })]);
        flushSync();

        expect(rows()[0].classList.contains('needs-explanation')).toBe(false);
        expect(rows()[0].querySelector('.item-status')).toBeNull();
    });
});

describe('phone card ends', () => {
    it('rounds the first and last rows of a short list', () => {
        setup();
        showFileListRows([
            makeFileRow({ id: '1', key: 'file:fs:1', selectionKey: 'file:1' }),
            makeFileRow({ id: '2', key: 'file:fs:2', selectionKey: 'file:2' }),
        ]);
        flushSync();

        const [first, last] = rows();
        expect(first.classList.contains('is-card-top')).toBe(true);
        expect(first.classList.contains('is-card-bottom')).toBe(false);
        expect(last.classList.contains('is-card-bottom')).toBe(true);
    });

    it('leaves the middle of a windowed list square', () => {
        setup();
        showFileListRows(longList(200));
        flushSync();

        Object.defineProperty(list, 'scrollTop', { value: 700, configurable: true });
        Object.defineProperty(list, 'clientHeight', { value: 680, configurable: true });
        list?.dispatchEvent(new Event('scroll'));
        flushSync();

        const rendered = rows();
        expect(rendered.length).toBeGreaterThan(0);
        expect(rendered.length).toBeLessThan(200);
        expect(rendered[0].dataset.rowKey).not.toBe('file:0');
        expect(rendered.some((row) => row.classList.contains('is-card-top'))).toBe(false);
        expect(rendered.some((row) => row.classList.contains('is-card-bottom'))).toBe(false);
    });

    it('reveals an offscreen logical row before attempting to focus it', async () => {
        setup();
        showFileListRows(longList(200));
        let scrollTop = 0;
        Object.defineProperty(list, 'scrollTop', {
            configurable: true,
            get: () => scrollTop,
            set: (value: number) => { scrollTop = value; },
        });
        Object.defineProperty(list, 'clientHeight', { value: 680, configurable: true });
        flushSync();
        expect(list?.querySelector('.drive-row[data-row-key="file:199"]')).toBeNull();

        window.dispatchEvent(new CustomEvent('tdrive:reveal-file-row', { detail: { key: 'file:199' } }));
        await Promise.resolve();
        flushSync();

        expect(scrollTop).toBeGreaterThan(0);
        expect(list?.querySelector('.drive-row[data-row-key="file:199"]')).not.toBeNull();
    });
});
