import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import FileList from './FileList.svelte';
import { showFileListRows, showFileListState, updateFileListRows } from './file-list-store';
import { resetFileSortState, setFileSortKey } from './file-sort-store';
import { setActiveFileRowKey, setSelectedFileRowKeys } from './row-state-store';
import type { FileListFileRow } from './types';

let app: Record<string, unknown> | null = null;
let host: HTMLElement | null = null;

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

function setup(): void {
    host = document.createElement('div');
    document.body.appendChild(host);
    app = mount(FileList, { target: host, props: {} });
    flushSync();
}

function row(): HTMLElement {
    const node = host?.querySelector<HTMLElement>('.drive-row[data-row-key="file:42"]');
    if (!node) throw new Error('missing file row');
    return node;
}

afterEach(async () => {
    resetFileSortState();
    showFileListState({
        stateKind: 'loading',
        title: 'Loading files',
    });
    setSelectedFileRowKeys([]);
    setActiveFileRowKey('');
    flushSync();
    if (app) await unmount(app);
    host?.remove();
    app = null;
    host = null;
});

describe('FileList DOM behavior', () => {
    it('updates selected and active accessibility state from stores', () => {
        setup();
        showFileListRows([makeFileRow()]);
        setSelectedFileRowKeys(['file:42']);
        setActiveFileRowKey('file:42');
        flushSync();

        expect(row().classList.contains('is-selected')).toBe(true);
        expect(row().classList.contains('is-keyboard-active')).toBe(true);
        expect(row().getAttribute('aria-selected')).toBe('true');
        expect(row().getAttribute('tabindex')).toBe('0');
    });

    it('uses grid rows and keeps row actions as native buttons', () => {
        setup();
        showFileListRows([makeFileRow({
            actions: [{
                kind: 'download',
                className: 'download',
                title: 'Download',
                label: 'Download',
            }],
        })]);
        flushSync();

        expect(row().getAttribute('role')).toBe('row');
        expect(row().querySelectorAll('[role="gridcell"]')).toHaveLength(4);
        expect(row().querySelector<HTMLButtonElement>('.action-icon.download')?.getAttribute('type')).toBe('button');
    });

    it('keeps keyed row elements stable across row-data updates', () => {
        setup();
        showFileListRows([makeFileRow({ metaLabel: 'Today' })]);
        setSelectedFileRowKeys(['file:42']);
        flushSync();
        const before = row();

        updateFileListRows((rows) => rows.map((item) => (
            item.kind === 'file'
                ? { ...item, metaLabel: 'Yesterday', uploaderChip: { label: 'Me 1m ago' } }
                : item
        )));
        flushSync();

        const after = row();
        expect(after).toBe(before);
        expect(after.classList.contains('is-selected')).toBe(true);
        expect(after.textContent).toContain('Yesterday');
        expect(after.querySelector('.uploader-chip')?.textContent).toBe('Me 1m ago');
    });

    it('routes row and action callbacks without bubbling action clicks to the row', () => {
        const onRowClick = vi.fn();
        const onDownload = vi.fn();
        setup();
        showFileListRows([
            makeFileRow({
                onClick: onRowClick,
                actions: [{
                    kind: 'download',
                    className: 'download',
                    title: 'Download',
                    label: 'Download',
                    onClick: onDownload,
                }],
            }),
        ]);
        flushSync();

        row().dispatchEvent(new MouseEvent('click', { bubbles: true }));
        host?.querySelector<HTMLButtonElement>('.action-icon.download')
            ?.dispatchEvent(new MouseEvent('click', { bubbles: true }));
        flushSync();

        expect(onRowClick).toHaveBeenCalledTimes(1);
        expect(onDownload).toHaveBeenCalledTimes(1);
    });

    it('reorders keyed rows when the sort state changes', () => {
        setup();
        showFileListRows([
            makeFileRow({ id: '2', key: 'file:2', selectionKey: 'file:2', name: 'beta.mp4', baseName: 'beta', uploadTime: 10 }),
            makeFileRow({ id: '1', key: 'file:1', selectionKey: 'file:1', name: 'alpha.mp4', baseName: 'alpha', uploadTime: 20 }),
        ]);
        setSelectedFileRowKeys(['file:2']);
        flushSync();

        setFileSortKey('name');
        flushSync();

        const rows = Array.from(host?.querySelectorAll<HTMLElement>('.drive-row') ?? []);
        expect(rows.map((item) => item.dataset.rowKey)).toEqual(['file:1', 'file:2']);
        expect(rows[1].classList.contains('is-selected')).toBe(true);
    });
    it('windows large row sets instead of mounting every row', () => {
        setup();
        showFileListRows(Array.from({ length: 128 }, (_, index) => makeFileRow({
            id: String(index),
            key: `file:fs:${index}`,
            selectionKey: `file:${index}`,
            name: `entry-${String(index).padStart(3, '0')}.txt`,
            baseName: `entry-${String(index).padStart(3, '0')}`,
            ariaLabel: `File: entry-${String(index).padStart(3, '0')}.txt`,
            uploadTime: index,
        })));
        flushSync();

        const rows = host?.querySelectorAll<HTMLElement>('.drive-row') ?? [];
        expect(rows.length).toBeLessThan(64);
        expect(rows[0]?.getAttribute('aria-rowindex')).toBe('2');
        expect(host?.querySelector('.drive-row[data-row-key="file:0"]')).toBeNull();
    });
});

describe('FileList phone rows', () => {
    const originalUrl = () => `${window.location.pathname}${window.location.search}`;

    it('renders the two-line row with meta, chip, lock and overflow, then checks in selection mode', () => {
        const previous = originalUrl();
        history.replaceState(null, '', '/?mobile=android');
        try {
            setup();
            showFileListRows([makeFileRow({
                name: 'Holiday Photos 2024 final version v2.zip',
                size: 1_400_000_000,
                uploadTime: Math.floor(Date.now() / 1000) - 5 * 3600,
                encrypted: true,
                uploaderChip: { label: 'Mara Okonkwo · 5h ago', firstName: 'Mara', initials: 'MO' },
            })]);
            flushSync();

            const node = row();
            expect(node.querySelector('.row-label-head')?.textContent).toBe('Holiday Photos 2024 final versio');
            expect(node.querySelector('.row-label-tail')?.textContent).toBe('n v2.zip');
            expect(node.querySelector('.row-sub-text')?.textContent).toBe('1.3 GB · 5 hours ago');
            expect(node.querySelector('.file-lock-badge')).not.toBeNull();
            expect(node.querySelector('.uploader-initials')?.textContent).toBe('MO');
            expect(node.querySelector('.uploader-chip')?.textContent?.trim()).toMatch(/Mara$/);
            expect(node.querySelector('button.row-more')?.getAttribute('aria-haspopup')).toBe('menu');
            expect(node.querySelector('.row-meta')).toBeNull();
            expect(node.querySelector('.row-check')).toBeNull();

            setSelectedFileRowKeys(['file:42']);
            flushSync();
            expect(row().querySelector('.row-check')).not.toBeNull();
            expect(row().classList.contains('is-selected')).toBe(true);
        } finally {
            history.replaceState(null, '', previous);
        }
    });

    it('keeps the desktop grid when not on a phone', () => {
        setup();
        showFileListRows([makeFileRow()]);
        flushSync();
        expect(row().querySelector('button.row-more')).toBeNull();
        expect(row().querySelectorAll('.row-meta')).toHaveLength(2);
    });
});
