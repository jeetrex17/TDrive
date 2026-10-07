import { describe, expect, it } from 'vitest';
import { pageJumpIndex, rangeSelectionKeys, typeAheadIndex } from './keyboard';

describe('typeAheadIndex', () => {
    const names = ['Apple', 'apricot', 'Banana', 'cherry', 'date'];

    it('matches case-insensitively from the start of the name', () => {
        expect(typeAheadIndex(names, 'ba', 0)).toBe(2);
        expect(typeAheadIndex(names, 'BA', 0)).toBe(2);
    });

    it('cycles to the next match for a single repeated character', () => {
        // From Apple, "a" lands on apricot; from apricot it wraps back to Apple.
        expect(typeAheadIndex(names, 'a', 0)).toBe(1);
        expect(typeAheadIndex(names, 'a', 1)).toBe(0);
    });

    it('keeps the current row when a longer buffer still matches it', () => {
        // Refining the buffer should not skip past the row already shown.
        expect(typeAheadIndex(names, 'apr', 1)).toBe(1);
    });

    it('wraps forward past the end of the list', () => {
        expect(typeAheadIndex(names, 'a', 4)).toBe(0);
    });

    it('returns -1 when nothing matches or there is nothing to search', () => {
        expect(typeAheadIndex(names, 'z', 0)).toBe(-1);
        expect(typeAheadIndex(names, '', 0)).toBe(-1);
        expect(typeAheadIndex([], 'a', 0)).toBe(-1);
    });
});

describe('pageJumpIndex', () => {
    it('moves a page of rows and clamps to the ends', () => {
        expect(pageJumpIndex(0, 100, 10, 1)).toBe(10);
        expect(pageJumpIndex(95, 100, 10, 1)).toBe(99);
        expect(pageJumpIndex(5, 100, 10, -1)).toBe(0);
    });

    it('moves at least one row and handles an empty list', () => {
        expect(pageJumpIndex(0, 100, 0, 1)).toBe(1);
        expect(pageJumpIndex(0, 0, 10, 1)).toBe(-1);
    });
});

describe('rangeSelectionKeys', () => {
    const rows = [{ selectionKey: 'a' }, { selectionKey: 'b' }, { selectionKey: 'c' }, { selectionKey: 'd' }];

    it('returns the inclusive span in row order regardless of direction', () => {
        expect(rangeSelectionKeys(rows, 1, 3)).toEqual(['b', 'c', 'd']);
        expect(rangeSelectionKeys(rows, 3, 1)).toEqual(['b', 'c', 'd']);
    });

    it('clamps out-of-range endpoints and handles an empty model', () => {
        expect(rangeSelectionKeys(rows, -5, 1)).toEqual(['a', 'b']);
        expect(rangeSelectionKeys(rows, 2, 99)).toEqual(['c', 'd']);
        expect(rangeSelectionKeys([], 0, 2)).toEqual([]);
    });
});
