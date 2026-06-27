import { afterEach, describe, expect, it } from 'vitest';
import { render } from 'svelte/server';
import FileList from './FileList.svelte';
import { showFileListRows, showFileListState } from './file-list-store';
import { setActiveFileRowKey, setSelectedFileRowKeys } from './row-state-store';
import type { FileListFileRow } from './types';

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

afterEach(() => {
    showFileListState({
        stateKind: 'loading',
        title: 'Loading files',
    });
    setSelectedFileRowKeys([]);
    setActiveFileRowKey('');
});

describe('FileList', () => {
    it('renders selected and active row state from stores', () => {
        showFileListRows([makeFileRow()]);
        setSelectedFileRowKeys(['file:42']);
        setActiveFileRowKey('file:42');

        const { body } = render(FileList);

        expect(body).toContain('class="file-row drive-row is-selected is-keyboard-active"');
        expect(body).toContain('data-row-key="file:42"');
        expect(body).toContain('aria-selected="true"');
        expect(body).toContain('tabindex="0"');
    });

    it('renders unselected rows as unfocused options', () => {
        showFileListRows([makeFileRow()]);

        const { body } = render(FileList);

        expect(body).toContain('class="file-row drive-row"');
        expect(body).toContain('aria-selected="false"');
        expect(body).toContain('tabindex="-1"');
    });

    it('renders uploader chips as escaped text', () => {
        showFileListRows([makeFileRow({
            uploaderChip: { label: 'A<b> 2m ago' },
        })]);

        const { body } = render(FileList);

        expect(body).toContain('<span class="uploader-chip">A&lt;b> 2m ago</span>');
        expect(body).not.toContain('<span class="uploader-chip">A<b>');
        expect(body).not.toContain('data-uploader-slot');
    });
});
