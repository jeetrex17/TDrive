import { beforeEach, describe, expect, it } from 'vitest';
import { get } from 'svelte/store';
import { busyRowIds, markRowsBusy } from './busy-rows';

beforeEach(() => {
    // Nothing exported clears the set; releasing every mark is the only way out,
    // which is the property the delete flow's `finally` depends on.
    markRowsBusy([])();
});

describe('marking rows busy', () => {
    it('marks and releases', () => {
        const release = markRowsBusy(['a', 'b']);
        expect(get(busyRowIds)).toEqual(new Set(['a', 'b']));
        release();
        expect(get(busyRowIds)).toEqual(new Set());
    });

    it('survives a second operation on the same row finishing first', () => {
        const first = markRowsBusy(['a']);
        const second = markRowsBusy(['a']);
        second();
        expect(get(busyRowIds).has('a')).toBe(true);
        first();
        expect(get(busyRowIds).has('a')).toBe(false);
    });

    it('ignores a release called twice, which would free somebody else’s mark', () => {
        const first = markRowsBusy(['a']);
        const second = markRowsBusy(['a']);
        first();
        first();
        expect(get(busyRowIds).has('a')).toBe(true);
        second();
        expect(get(busyRowIds).has('a')).toBe(false);
    });

    it('drops empty ids rather than marking a row that does not exist', () => {
        markRowsBusy(['', 'a'])();
        expect(get(busyRowIds)).toEqual(new Set());
    });
});
