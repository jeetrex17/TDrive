import { get, writable } from 'svelte/store';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { navDirection } from './nav-direction';

afterEach(() => { vi.useRealTimers(); });

describe('nav direction', () => {
    it('reports forward into a folder, back out of one, then settles', () => {
        vi.useFakeTimers();
        const path = writable<string[]>([]);
        const direction = navDirection(path, 280);
        const seen: unknown[] = [];
        const stop = direction.subscribe((value) => seen.push(value));

        path.set(['Documents']);
        expect(get(direction)).toBe('forward');
        vi.advanceTimersByTime(280);
        expect(get(direction)).toBeNull();

        path.set([]);
        expect(get(direction)).toBe('back');
        stop();
        expect(seen).toEqual([null, 'forward', null, 'back']);
    });

    it('does not animate the first path it sees', () => {
        const path = writable(['Documents', 'Taxes']);
        expect(get(navDirection(path))).toBeNull();
    });
});
