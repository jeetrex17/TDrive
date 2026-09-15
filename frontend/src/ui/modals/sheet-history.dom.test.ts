// Android BACK ownership for sheets: the topmost open sheet consumes a BACK
// press (popstate) before the shell pops a folder level, and closing a sheet
// through its own controls unwinds only its own history entry.

import { afterEach, describe, expect, it, vi } from 'vitest';
import { closeTopSheetFromHistory, hasOpenSheet, pushSheet } from './sheet-history';

afterEach(() => {
    // Drain any sheet left registered so module state does not leak.
    while (closeTopSheetFromHistory()) { /* keep closing */ }
});

describe('sheet-history', () => {
    it('reports no consumption when nothing is open', () => {
        expect(hasOpenSheet()).toBe(false);
        expect(closeTopSheetFromHistory()).toBe(false);
    });

    it('closes the open sheet on a BACK press and reports it consumed', () => {
        const close = vi.fn();
        pushSheet(close);
        expect(hasOpenSheet()).toBe(true);

        expect(closeTopSheetFromHistory()).toBe(true);
        expect(close).toHaveBeenCalledTimes(1);
        expect(hasOpenSheet()).toBe(false);
    });

    it('closes stacked sheets top first', () => {
        const order: string[] = [];
        pushSheet(() => order.push('bottom'));
        pushSheet(() => order.push('top'));

        closeTopSheetFromHistory();
        closeTopSheetFromHistory();

        expect(order).toEqual(['top', 'bottom']);
    });

    it('a BACK press (popstate) closes the top sheet', () => {
        const close = vi.fn();
        pushSheet(close);

        window.dispatchEvent(new Event('popstate'));

        expect(close).toHaveBeenCalledTimes(1);
        expect(hasOpenSheet()).toBe(false);
    });

    it('release removes the entry so a later BACK does not close it again', () => {
        const close = vi.fn();
        const handle = pushSheet(close);

        handle.release();
        expect(hasOpenSheet()).toBe(false);
        // Its own close path never fires close(); only BACK does.
        expect(close).not.toHaveBeenCalled();
        expect(closeTopSheetFromHistory()).toBe(false);
    });
});
