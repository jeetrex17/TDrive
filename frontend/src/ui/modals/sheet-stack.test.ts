// The stack Android BACK consults before anything underneath a sheet.
import { afterEach, describe, expect, it, vi } from 'vitest';
import { closeTopSheet, hasOpenSheet, pushSheet } from './sheet-stack';

afterEach(() => {
    // Drain anything a failing case left behind so the stack never leaks.
    while (closeTopSheet());
});

describe('sheet stack', () => {
    it('reports no consumption when nothing is open', () => {
        expect(hasOpenSheet()).toBe(false);
        expect(closeTopSheet()).toBe(false);
    });

    it('closes the open sheet and reports the press consumed', () => {
        const close = vi.fn();
        pushSheet(close);

        expect(hasOpenSheet()).toBe(true);
        expect(closeTopSheet()).toBe(true);
        expect(close).toHaveBeenCalledOnce();
        expect(hasOpenSheet()).toBe(false);
    });

    it('closes stacked sheets top first', () => {
        const order: string[] = [];
        pushSheet(() => order.push('under'));
        pushSheet(() => order.push('over'));

        expect(closeTopSheet()).toBe(true);
        expect(closeTopSheet()).toBe(true);
        expect(order).toEqual(['over', 'under']);
    });

    it('release drops a sheet that closed through its own controls', () => {
        const close = vi.fn();
        const handle = pushSheet(close);

        handle.release();

        expect(hasOpenSheet()).toBe(false);
        expect(closeTopSheet()).toBe(false);
        expect(close).not.toHaveBeenCalled();
    });

    it('release of a buried sheet leaves the one above it on top', () => {
        const closeUnder = vi.fn();
        const closeOver = vi.fn();
        const under = pushSheet(closeUnder);
        pushSheet(closeOver);

        under.release();

        expect(closeTopSheet()).toBe(true);
        expect(closeOver).toHaveBeenCalledOnce();
        expect(closeUnder).not.toHaveBeenCalled();
    });
});
