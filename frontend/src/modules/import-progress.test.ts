import { describe, expect, it } from 'vitest';
import {
    createImportProgress,
    reduceImportProgress,
    type ImportProgress,
} from './import-progress';

describe('import progress reducer', () => {
    it('folds high-cardinality aggregate snapshots into constant-size immutable state', () => {
        const initial = createImportProgress();
        const first = reduceImportProgress(initial, {
            total: 10_000,
            done: 1,
            failed: 0,
            progress: 0.01,
        });

        expect(first).not.toBe(initial);
        expect(initial).toEqual({ total: 0, done: 0, failed: 0, progress: 0, bytes: 0, active: [], activeCount: 0 });

        let current: ImportProgress = first;
        for (let done = 2; done <= 10_000; done++) {
            current = reduceImportProgress(current, {
                total: 10_000,
                done,
                failed: 0,
                progress: done / 100,
                bytes: done * 1_000,
                // Three files in flight at a time, whatever the size of the
                // import: this is the shape the backend actually sends.
                active: [
                    { id: done, name: `file-${done}.jpg`, percent: 10, size: 1_000 },
                    { id: done + 1, name: `file-${done + 1}.jpg`, percent: 40, size: 1_000 },
                    { id: done + 2, name: `file-${done + 2}.jpg`, percent: 70, size: 1_000 },
                ],
                activeCount: 3,
            });
        }

        // The reducer receives backend aggregates, so its retained state cannot
        // grow with the number of files (the previous implementation kept one
        // Map entry per active upload and repeatedly iterated the whole Map).
        expect(Object.keys(current).sort())
            .toEqual(['active', 'activeCount', 'bytes', 'done', 'failed', 'progress', 'total']);
        expect(current.active).toHaveLength(3);
        expect(current).toMatchObject({ total: 10_000, done: 10_000, failed: 0, progress: 100, bytes: 10_000_000 });
    });

    it('clamps finite values and ignores non-finite regressions', () => {
        const clamped = reduceImportProgress(createImportProgress(), {
            total: 8,
            done: 2,
            failed: -4,
            progress: -20,
        });

        expect(clamped).toEqual({ total: 8, done: 2, failed: 0, progress: 0, bytes: 0, active: [], activeCount: 0 });

        const unchanged = reduceImportProgress(clamped, {
            total: Number.NaN,
            done: Number.POSITIVE_INFINITY,
            failed: Number.NEGATIVE_INFINITY,
            progress: Number.NaN,
        });
        expect(unchanged).toEqual(clamped);

        const upperBound = reduceImportProgress(clamped, {
            total: 8,
            done: 2,
            failed: 0,
            progress: 140,
        });
        expect(upperBound.progress).toBe(100);
    });

    it('does not move backward when a delayed snapshot arrives', () => {
        const current = reduceImportProgress(createImportProgress(), {
            total: 20,
            done: 8,
            failed: 2,
            progress: 54,
        });
        const afterDelayed = reduceImportProgress(current, {
            total: 18,
            done: 5,
            failed: 1,
            progress: 31,
        });

        expect(afterDelayed).toEqual(current);
    });

    it('forces terminal progress to 100 when every item is accounted for', () => {
        const complete = reduceImportProgress(createImportProgress(), {
            total: 10,
            done: 7,
            failed: 3,
            progress: 83,
        });

        expect(complete).toEqual({ total: 10, done: 7, failed: 3, progress: 100, bytes: 0, active: [], activeCount: 0 });
    });

    it('creates an independent zero state for the next import', () => {
        const previous = reduceImportProgress(createImportProgress(), {
            total: 2,
            done: 2,
            failed: 0,
            progress: 100,
        });
        const reset = createImportProgress();

        expect(reset).not.toBe(previous);
        expect(reset).toEqual({ total: 0, done: 0, failed: 0, progress: 0, bytes: 0, active: [], activeCount: 0 });
    });

    it('caps the named files at what a row can show, and says how many it left out', () => {
        const busy = reduceImportProgress(createImportProgress(), {
            total: 100,
            done: 4,
            failed: 0,
            progress: 4,
            bytes: 400,
            active: Array.from({ length: 8 }, (_, index) => ({
                id: index, name: `busy-${index}.mov`, percent: index * 10, size: 100,
            })),
            activeCount: 8,
        });

        expect(busy.active).toHaveLength(4);
        expect(busy.active.map((item) => item.key)).toEqual(['0', '1', '2', '3']);
        expect(busy.activeCount).toBe(8);
    });

    it('replaces the in-flight list rather than accumulating it, and bounds what reaches the DOM', () => {
        const first = reduceImportProgress(createImportProgress(), {
            total: 4, done: 0, failed: 0, progress: 1, bytes: 0,
            active: [{ id: 1, name: 'a'.repeat(400), percent: 10, size: 10 }],
            activeCount: 1,
        });
        expect(first.active[0].name.length).toBeLessThanOrEqual(120);

        // The second file replaced the first; a list that grew instead would be
        // one entry per file of the whole import by the end of it.
        const second = reduceImportProgress(first, {
            total: 4, done: 1, failed: 0, progress: 25, bytes: 10,
            active: [{ id: 2, name: 'b.txt', percent: 5, size: 10 }],
            activeCount: 1,
        });
        expect(second.active).toEqual([{ key: '2', name: 'b.txt', progress: 5, total: 10 }]);
    });
});
