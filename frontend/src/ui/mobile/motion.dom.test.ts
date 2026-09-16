// behavior is an argument, not a property, so no stylesheet can turn a smooth
// scroll off. This is the one place that asks.
import { afterEach, describe, expect, it, vi } from 'vitest';
import { scrollBehavior } from './motion';

const real = window.matchMedia;

afterEach(() => {
    window.matchMedia = real;
});

function reducedMotion(matches: boolean): void {
    window.matchMedia = vi.fn(() => ({ matches })) as unknown as typeof window.matchMedia;
}

describe('scrollBehavior', () => {
    it('glides by default', () => {
        reducedMotion(false);
        expect(scrollBehavior()).toBe('smooth');
    });

    it('arrives without the travel under Reduce Motion', () => {
        reducedMotion(true);
        expect(scrollBehavior()).toBe('auto');
    });
});
