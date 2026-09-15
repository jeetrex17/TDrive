import { describe, expect, it } from 'vitest';
import { relativeTimeLabel, rowMetaLine, splitRowLabel } from './row-meta';
import type { FileListFileRow, FolderListRow } from './types';

const NOW_MS = Date.UTC(2026, 8, 15, 12, 0, 0);
const NOW = Math.floor(NOW_MS / 1000);

function fileRow(overrides: Partial<FileListFileRow> = {}): FileListFileRow {
    return {
        kind: 'file',
        key: 'file:fs:1',
        selectionKey: 'file:1',
        id: '1',
        name: 'plan.pdf',
        baseName: 'plan',
        ext: 'PDF',
        source: 'fs',
        parentId: '',
        size: 2_100_000,
        metaLabel: '',
        sizeLabel: '',
        ariaLabel: 'File: plan.pdf',
        uploaderID: 0,
        uploadTime: NOW - 2 * 3600,
        encrypted: false,
        canDelete: true,
        canRename: true,
        actions: [],
        ...overrides,
    };
}

function folderRow(overrides: Partial<FolderListRow> = {}): FolderListRow {
    return {
        kind: 'folder',
        key: 'folder:a',
        selectionKey: 'folder:a',
        id: 'a',
        name: 'Design',
        parentId: '',
        metaLabel: '',
        sizeLabel: '',
        ariaLabel: 'Folder: Design',
        size: 0,
        modifiedTime: 0,
        actions: [],
        ...overrides,
    };
}

describe('relativeTimeLabel', () => {
    it('reads as spoken time up to a week, then the calendar date', () => {
        expect(relativeTimeLabel(0, NOW_MS)).toBe('');
        expect(relativeTimeLabel(NOW - 20, NOW_MS)).toBe('Just now');
        expect(relativeTimeLabel(NOW - 60, NOW_MS)).toBe('1 minute ago');
        expect(relativeTimeLabel(NOW - 5 * 60, NOW_MS)).toBe('5 minutes ago');
        expect(relativeTimeLabel(NOW - 3600, NOW_MS)).toBe('1 hour ago');
        expect(relativeTimeLabel(NOW - 2 * 3600, NOW_MS)).toBe('2 hours ago');
        expect(relativeTimeLabel(NOW - 26 * 3600, NOW_MS)).toBe('Yesterday');
        expect(relativeTimeLabel(NOW - 3 * 86_400, NOW_MS)).toBe('3 days ago');
        expect(relativeTimeLabel(NOW - 9 * 86_400, NOW_MS)).toMatch(/2026/);
    });
});

describe('rowMetaLine', () => {
    it('joins size and age with one middle dot', () => {
        expect(rowMetaLine(fileRow(), NOW_MS)).toBe('2 MB · 2 hours ago');
    });

    it('leaves a folder without stats blank and drops unknown halves', () => {
        expect(rowMetaLine(folderRow(), NOW_MS)).toBe('');
        expect(rowMetaLine(folderRow({ size: 1_280_000_000, modifiedTime: NOW - 2 * 86_400 }), NOW_MS)).toBe('1.2 GB · 2 days ago');
        expect(rowMetaLine(folderRow({ modifiedTime: NOW - 2 * 86_400 }), NOW_MS)).toBe('2 days ago');
    });
});

describe('splitRowLabel', () => {
    it('keeps the extension and the last characters of the base in the tail', () => {
        expect(splitRowLabel('Holiday Photos 2024 final version v2.zip')).toEqual({ head: 'Holiday Photos 2024 final versio', tail: 'n v2.zip' });
        expect(splitRowLabel('Project Plan.pdf')).toEqual({ head: 'Project ', tail: 'Plan.pdf' });
    });

    it('does not split names without an extension or shorter than the tail', () => {
        expect(splitRowLabel('Design Assets')).toEqual({ head: 'Design Assets', tail: '' });
        expect(splitRowLabel('.env')).toEqual({ head: '.env', tail: '' });
        expect(splitRowLabel('ab.md')).toEqual({ head: '', tail: 'ab.md' });
    });
});
