import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { TrashEntry } from '../../api/trash';

vi.mock('./controller', () => ({
    askPurgeTrashEntry: vi.fn(),
    loadTrash: vi.fn(),
    restoreEntry: vi.fn(),
    trashEntries: { subscribe: () => () => {} },
}));

const { trashRow } = await import('./view');

const NOW = 1_700_000_000_000;
const DAY = 86_400_000;

function entry(overrides: Partial<TrashEntry> = {}): TrashEntry {
    return {
        objectId: 'f:2615', kind: 'file', name: 'IMG_0042.jpg', parentPath: 'Trips',
        size: 2048, deletedAt: NOW, purgeAfter: NOW + 6 * DAY,
        revision: 4, encrypted: false,
        ...overrides,
    };
}

describe('trashRow', () => {
    beforeEach(() => vi.clearAllMocks());

    it('addresses the thumbnail with the live revision, which is what the backend serves', () => {
        const row = trashRow(entry(), NOW, 41);
        expect(row.kind).toBe('file');
        if (row.kind !== 'file') return;
        // The msg id comes out of the object id; the revision is the entry's
        // live one. A stale revision is refused by the rendition path, so this
        // is the difference between a picture and a placeholder.
        expect(row.thumbnail).toEqual({ channelId: 41, fileId: 2615, revision: 4 });
    });

    // Whatever the drive can picture, the trash pictures; whatever it cannot
    // (a .txt, and today a .HEIC too), the trash leaves as an icon rather than
    // asking for a rendition that would come back empty.
    it('gives a non-image no thumbnail rather than one that cannot resolve', () => {
        const row = trashRow(entry({ name: 'notes.txt' }), NOW, 41);
        if (row.kind !== 'file') throw new Error('expected a file row');
        expect(row.thumbnail).toBeUndefined();
        const heic = trashRow(entry({ name: 'IMG_0042.HEIC' }), NOW, 41);
        if (heic.kind !== 'file') throw new Error('expected a file row');
        expect(heic.thumbnail).toBeUndefined();
    });

    it('reports the countdown in the column the drive uses for the date', () => {
        const row = trashRow(entry({ purgeAfter: NOW + 6 * DAY }), NOW, 41);
        expect(row.metaLabel).toBe('6 days left');
        const expiring = trashRow(entry({ purgeAfter: NOW - DAY }), NOW, 41);
        expect(expiring.metaLabel).toBe('Purging soon');
    });

    it('offers restore before delete forever, and nothing else', () => {
        const row = trashRow(entry(), NOW, 41);
        expect(row.actions.map((action) => action.kind)).toEqual(['restore', 'purge']);
    });

    it('refuses the drive mutations that have no meaning in the trash', () => {
        const row = trashRow(entry(), NOW, 41);
        if (row.kind !== 'file') throw new Error('expected a file row');
        expect(row.canDelete).toBe(false);
        expect(row.canRename).toBe(false);
        // The chip answers "who put this here", which is not a trash question.
        expect(row.uploaderChip).toBeNull();
    });

    it('keeps where an item came from reachable to a screen reader', () => {
        const row = trashRow(entry({ parentPath: 'Trips/Iceland' }), NOW, 41);
        expect(row.ariaLabel).toContain('Trips/Iceland');
        expect(row.ariaLabel).toContain('6 days left');
        // An item deleted from the drive's root still says where it was.
        expect(trashRow(entry({ parentPath: '' }), NOW, 41).ariaLabel).toContain('Drive root');
    });

    it('renders a trashed folder as a folder, weightless and openable by nothing', () => {
        const row = trashRow(entry({ objectId: 'd:abc', kind: 'folder', name: 'Old notes' }), NOW, 41);
        expect(row.kind).toBe('folder');
        if (row.kind !== 'folder') return;
        expect(row.sizeLabel).toBe('—');
        expect(row.actions.map((action) => action.kind)).toEqual(['restore', 'purge']);
        expect(row.onDoubleClick).toBeUndefined();
    });

    it('keys rows apart from the drive rows of the same objects', () => {
        // A restored file can be in both lists across a refresh; identical keys
        // would let one list's row state be applied to the other's.
        const row = trashRow(entry(), NOW, 41);
        expect(row.key).toBe('trash:f:2615');
    });
});
