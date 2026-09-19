import { describe, expect, it } from 'vitest';
import { rowOffset, rowWindowFor, type RowMetrics } from './row-window';

const EVEN: RowMetrics = { rowHeight: 68, tallRowHeight: 68, tallIndices: [] };
// Two rows explaining a failed transfer, 16px taller than the rest.
const MIXED: RowMetrics = { rowHeight: 68, tallRowHeight: 84, tallIndices: [3, 50] };

describe('rowOffset', () => {
    it('stacks even rows', () => {
        expect(rowOffset(0, EVEN)).toBe(0);
        expect(rowOffset(10, EVEN)).toBe(680);
    });

    it('carries the extra height of every taller row above it', () => {
        expect(rowOffset(3, MIXED)).toBe(3 * 68);
        expect(rowOffset(4, MIXED)).toBe(4 * 68 + 16);
        expect(rowOffset(51, MIXED)).toBe(51 * 68 + 32);
    });
});

describe('rowWindowFor', () => {
    it('renders around the viewport with slack on both sides', () => {
        const window = rowWindowFor(200, 68 * 40, 680, 8, EVEN);
        expect(window.start).toBe(32);
        expect(window.before).toBe(32 * 68);
        // Every row is accounted for: spacers plus rendered rows are the list.
        expect(window.before + (window.end - window.start) * 68 + window.after).toBe(200 * 68);
    });

    it('lands on the row the scroll position is actually showing when heights differ', () => {
        // Past both taller rows, so the naive scrollTop / rowHeight is two rows
        // and 32px out -- which is the jump this exists to stop.
        const scrollTop = rowOffset(80, MIXED);
        const window = rowWindowFor(200, scrollTop, 680, 8, MIXED);
        expect(window.start).toBe(72);
        expect(window.before).toBe(rowOffset(72, MIXED));
        expect(window.before + window.after
            + (rowOffset(window.end, MIXED) - rowOffset(window.start, MIXED))).toBe(rowOffset(200, MIXED));
    });

    it('keeps the ends of the list whole', () => {
        const top = rowWindowFor(200, 0, 680, 8, MIXED);
        expect(top.start).toBe(0);
        expect(top.before).toBe(0);

        const bottom = rowWindowFor(200, rowOffset(200, MIXED), 680, 8, MIXED);
        expect(bottom.end).toBe(200);
        expect(bottom.after).toBe(0);
    });

    it('handles an empty list', () => {
        expect(rowWindowFor(0, 0, 680, 8, EVEN)).toEqual({ before: 0, start: 0, end: 0, after: 0 });
    });
});
