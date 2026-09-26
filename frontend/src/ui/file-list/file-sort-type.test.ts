// Sorting by type, which exists so a reader can say "show me the PDFs
// together" -- so the test that matters is whether the clusters form AND stay
// readable inside themselves.
import { describe, expect, it } from 'vitest';
import { nextFileSortState, sortFileListRows } from './file-sort';
import type { FileListFileRow, FolderListRow } from './types';

function file(name: string, ext: string): FileListFileRow {
    return {
        kind: 'file',
        key: `file:${name}`,
        selectionKey: `file:${name}`,
        id: name,
        name,
        baseName: name.replace(/\.[^.]+$/, ''),
        ext,
        source: 'fs',
        parentId: '',
        metaLabel: '',
        sizeLabel: '',
        ariaLabel: `File: ${name}`,
        size: 0,
        uploadTime: 0,
        uploaderID: 0,
        encrypted: false,
        canDelete: true,
        canRename: true,
        actions: [],
    };
}

function folder(name: string): FolderListRow {
    return {
        kind: 'folder',
        key: `folder:${name}`,
        selectionKey: `folder:${name}`,
        id: name,
        name,
        parentId: '',
        metaLabel: '',
        sizeLabel: '',
        size: 0,
        modifiedTime: 0,
        ariaLabel: `Folder: ${name}`,
        actions: [],
    };
}

describe('sorting by type', () => {
    it('clusters by extension and reads by name inside each cluster', () => {
        const sorted = sortFileListRows(
            [file('b.pdf', 'pdf'), file('z.jpg', 'jpg'), file('a.pdf', 'pdf'), file('m.jpg', 'jpg')],
            { key: 'type', direction: 'asc' },
        );
        // Grouping alone is not the whole promise: twelve PDFs left in
        // arbitrary order inside their cluster is half a sort.
        expect(sorted.map((row) => row.name)).toEqual(['m.jpg', 'z.jpg', 'a.pdf', 'b.pdf']);
    });

    it('keeps folders ahead of files, in name order', () => {
        const sorted = sortFileListRows(
            [file('doc.pdf', 'pdf'), folder('Beta'), folder('Alpha')],
            { key: 'type', direction: 'asc' },
        );
        expect(sorted.map((row) => row.name)).toEqual(['Alpha', 'Beta', 'doc.pdf']);
    });

    it('sorts files without an extension together rather than dropping them', () => {
        const sorted = sortFileListRows(
            [file('notes.md', 'md'), file('LICENSE', ''), file('README', '')],
            { key: 'type', direction: 'asc' },
        );
        expect(sorted.map((row) => row.name)).toEqual(['LICENSE', 'README', 'notes.md']);
    });

    it('opens ascending, because grouping is the point of choosing it', () => {
        expect(nextFileSortState({ key: 'name', direction: 'asc' }, 'type'))
            .toEqual({ key: 'type', direction: 'asc' });
    });

    it('still toggles direction when picked twice', () => {
        expect(nextFileSortState({ key: 'type', direction: 'asc' }, 'type'))
            .toEqual({ key: 'type', direction: 'desc' });
    });
});
