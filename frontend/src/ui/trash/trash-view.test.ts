import { describe, expect, it } from 'vitest';
import type { TrashEntry } from '../../api/trash';
import { entryOrigin, originLabel, purgeCountdown, sortedByDeletion, trashSummary } from './trash-view';

const HOUR = 3_600_000;
const DAY = 24 * HOUR;
const NOW = 1_700_000_000_000;

function entry(overrides: Partial<TrashEntry> = {}): TrashEntry {
    return {
        objectId: 'f:1', kind: 'file', name: 'photo.jpg', parentPath: '',
        size: 0, deletedAt: NOW, purgeAfter: NOW + 7 * DAY,
        revision: 0, encrypted: false, ...overrides,
    };
}

describe('sortedByDeletion', () => {
    it('puts the most recent deletion first and breaks ties by name', () => {
        const rows = [
            entry({ objectId: 'a', name: 'zulu', deletedAt: NOW - DAY }),
            entry({ objectId: 'b', name: 'bravo', deletedAt: NOW }),
            entry({ objectId: 'c', name: 'alpha', deletedAt: NOW }),
        ];
        expect(sortedByDeletion(rows).map((row) => row.objectId)).toEqual(['c', 'b', 'a']);
    });

    it('leaves the caller\'s list untouched', () => {
        const rows = [entry({ objectId: 'a', deletedAt: 1 }), entry({ objectId: 'b', deletedAt: 2 })];
        sortedByDeletion(rows);
        expect(rows.map((row) => row.objectId)).toEqual(['a', 'b']);
    });
});

describe('purgeCountdown', () => {
    it('reads the remaining time the way a person would say it', () => {
        expect(purgeCountdown(NOW + 7 * DAY, NOW).label).toBe('7 days left');
        expect(purgeCountdown(NOW + 2 * DAY, NOW).label).toBe('2 days left');
        const noon = new Date(2026, 0, 10, 12, 0, 0).getTime();
        expect(purgeCountdown(noon + 30 * HOUR, noon).label).toBe('Purges tomorrow');
        // The same 30 hours from late evening lands two dates away.
        const evening = new Date(2026, 0, 10, 23, 0, 0).getTime();
        expect(purgeCountdown(evening + 30 * HOUR, evening).label).toBe('1 day left');
        expect(purgeCountdown(NOW + 5 * HOUR, NOW).label).toBe('5 hours left');
        expect(purgeCountdown(NOW + HOUR, NOW).label).toBe('1 hour left');
        expect(purgeCountdown(NOW + 20 * 60_000, NOW).label).toBe('Less than an hour left');
    });

    it('treats an elapsed or missing deadline as imminent, never as a negative', () => {
        expect(purgeCountdown(NOW - DAY, NOW)).toEqual({ label: 'Purging soon', urgent: true });
        expect(purgeCountdown(0, NOW)).toEqual({ label: 'Purging soon', urgent: true });
    });

    it('marks only the last two days urgent', () => {
        expect(purgeCountdown(NOW + 3 * DAY, NOW).urgent).toBe(false);
        expect(purgeCountdown(NOW + 47 * HOUR, NOW).urgent).toBe(true);
    });
});

describe('originLabel', () => {
    it('names the drive root when the item came from nowhere deeper', () => {
        expect(originLabel('')).toBe('Drive root');
        expect(originLabel('   ')).toBe('Drive root');
        expect(originLabel('Trips/Iceland')).toBe('Trips/Iceland');
    });
});

describe('entryOrigin', () => {
    it('says where it was and how big it was', () => {
        expect(entryOrigin(entry({ parentPath: 'Trips', size: 2048 }))).toBe('Trips · 2 KB');
    });

    it('leaves out a size a folder does not have', () => {
        expect(entryOrigin(entry({ kind: 'folder', size: 0 }))).toBe('Drive root');
    });
});

describe('trashSummary', () => {
    it('counts the items and what they are holding on to', () => {
        expect(trashSummary([entry({ size: 1024 }), entry({ objectId: 'f:2', size: 1024 })])).toBe('2 items · 2 KB');
        expect(trashSummary([entry({ size: 0 })])).toBe('1 item');
        expect(trashSummary([])).toBe('');
    });
});
