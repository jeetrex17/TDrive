import { describe, expect, it } from 'vitest';
import {
    claimGesture,
    EDGE_GUARD_PX,
    rubberband,
    shouldOpen,
    swipeOffset,
    SWIPE_SLOP_PX,
    trailingEdgeGuard,
} from './swipe-actions';

// Rows sit inset from the screen edge, so a normal press lands well clear of
// the system band.
const MID = 200;

describe('claimGesture', () => {
    it('waits before deciding anything', () => {
        expect(claimGesture(0, 0, MID)).toBe('undecided');
        expect(claimGesture(-4, 3, MID)).toBe('undecided');
        expect(claimGesture(-(SWIPE_SLOP_PX - 1), 0, MID)).toBe('undecided');
    });

    it('gives clearly sideways movement to the row', () => {
        expect(claimGesture(-40, 2, MID)).toBe('row');
        expect(claimGesture(-20, 5, MID)).toBe('row');
    });

    it('gives vertical movement to the list', () => {
        expect(claimGesture(-2, 40, MID)).toBe('list');
        expect(claimGesture(0, -30, MID)).toBe('list');
    });

    it('gives an ambiguous diagonal to the list, so scrolling never feels sticky', () => {
        // Equal parts sideways and down during a fast flick-scroll: the
        // scroller keeps it rather than the row stealing it.
        expect(claimGesture(-30, 30, MID)).toBe('list');
        expect(claimGesture(-30, 25, MID)).toBe('list');
    });

    it('never takes a gesture born in the system back-swipe band', () => {
        // Unambiguously horizontal, but it started where iOS owns the screen.
        expect(claimGesture(-80, 0, 0)).toBe('list');
        expect(claimGesture(-80, 0, EDGE_GUARD_PX)).toBe('list');
        expect(claimGesture(-80, 0, EDGE_GUARD_PX + 1)).toBe('row');
    });

    it('ignores a rightward drag, because a closed row has nothing on that side', () => {
        expect(claimGesture(60, 0, MID)).toBe('list');
    });

    it('gives up the trailing band too where the system reserves it', () => {
        // Android's gesture navigation answers back on both edges. It takes the
        // touches away mid-drag, which left the row half open behind the app
        // going back.
        const edge = trailingEdgeGuard(400, true);

        expect(claimGesture(-80, 0, 399, edge)).toBe('list');
        expect(claimGesture(-80, 0, edge, edge)).toBe('list');
        expect(claimGesture(-80, 0, edge - 1, edge)).toBe('row');
    });

    it('keeps the trailing band where nothing reserves it', () => {
        // On iOS the last 24px is the row's own overflow button, and guarding
        // it would cost a real gesture to prevent nothing.
        expect(trailingEdgeGuard(400, false)).toBe(0);
        expect(claimGesture(-80, 0, 399)).toBe('row');
    });
});

describe('swipeOffset', () => {
    const OPEN = 140;

    it('follows the finger exactly while the actions are being revealed', () => {
        expect(swipeOffset(-50, OPEN)).toBe(-50);
        expect(swipeOffset(-140, OPEN)).toBe(-140);
    });

    it('resists past the actions instead of stopping dead', () => {
        const past = swipeOffset(-240, OPEN);
        expect(past).toBeLessThan(-OPEN);
        // Resisted, so it has not travelled the full extra 100px.
        expect(past).toBeGreaterThan(-240);
    });

    it('stays put when pulled the other way', () => {
        expect(swipeOffset(60, OPEN)).toBe(0);
        expect(swipeOffset(0, OPEN)).toBe(0);
    });
});

describe('rubberband', () => {
    it('gives less the further past the edge the finger goes', () => {
        const near = rubberband(20, 140);
        const far = rubberband(200, 140);
        expect(near).toBeLessThan(20);
        expect(far).toBeLessThan(200);
        expect(far / 200).toBeLessThan(near / 20);
    });
});

describe('shouldOpen', () => {
    const OPEN = 140;

    it('opens on a flick that barely moved', () => {
        expect(shouldOpen(-20, -900, OPEN)).toBe(true);
    });

    it('closes on a flick back, even from almost fully open', () => {
        expect(shouldOpen(-130, 900, OPEN)).toBe(false);
    });

    it('leaves a slow drag where it was parked', () => {
        expect(shouldOpen(-100, 0, OPEN)).toBe(true);
        expect(shouldOpen(-40, 0, OPEN)).toBe(false);
    });

    it('treats the halfway mark as the line when there is no real speed', () => {
        expect(shouldOpen(-OPEN / 2, 0, OPEN)).toBe(true);
        expect(shouldOpen(-OPEN / 2 + 1, 0, OPEN)).toBe(false);
    });

    it('cannot open a row with no actions', () => {
        expect(shouldOpen(-100, -900, 0)).toBe(false);
    });
});
