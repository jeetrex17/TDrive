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
        expect(initial).toEqual({ total: 0, done: 0, failed: 0, progress: 0 });

        let current: ImportProgress = first;
        for (let done = 2; done <= 10_000; done++) {
            current = reduceImportProgress(current, {
                total: 10_000,
                done,
                failed: 0,
                progress: done / 100,
            });
        }

        // The reducer receives backend aggregates, so its retained state cannot
        // grow with the number of files (the previous implementation kept one
        // Map entry per active upload and repeatedly iterated the whole Map).
        expect(Object.keys(current).sort()).toEqual(['done', 'failed', 'progress', 'total']);
        expect(current).toEqual({ total: 10_000, done: 10_000, failed: 0, progress: 100 });
    });

    it('clamps finite values and ignores non-finite regressions', () => {
        const clamped = reduceImportProgress(createImportProgress(), {
            total: 8,
            done: 2,
            failed: -4,
            progress: -20,
        });

        expect(clamped).toEqual({ total: 8, done: 2, failed: 0, progress: 0 });

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

        expect(complete).toEqual({ total: 10, done: 7, failed: 3, progress: 100 });
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
        expect(reset).toEqual({ total: 0, done: 0, failed: 0, progress: 0 });
    });
});
