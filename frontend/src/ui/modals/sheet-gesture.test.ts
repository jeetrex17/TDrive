import { describe, expect, it } from 'vitest';
import {
    createSheetDrag,
    projectOffset,
    rubberband,
    sheetOffset,
    shouldDismiss,
} from './sheet-gesture';

describe('sheet drag physics', () => {
    it('measures speed across the whole gesture, not the last event', () => {
        const drag = createSheetDrag();
        drag.start({ clientY: 0, timeStamp: 0 });
        drag.track({ clientY: 30, timeStamp: 50 });
        drag.track({ clientY: 60, timeStamp: 100 });
        // 60px in 100ms is 600px/s.
        expect(drag.velocity()).toBeCloseTo(600, 0);
    });

    it('reads a finger that stopped before lifting as slow, not as a flick', () => {
        const drag = createSheetDrag();
        drag.start({ clientY: 0, timeStamp: 0 });
        drag.track({ clientY: 100, timeStamp: 100 });
        // Four samples at rest push the fast opening out of the window.
        for (const t of [200, 300, 400, 500]) drag.track({ clientY: 100, timeStamp: t });
        expect(Math.abs(drag.velocity())).toBeLessThan(100);
    });

    it('reports no speed before there is anything to measure', () => {
        const drag = createSheetDrag();
        expect(drag.velocity()).toBe(0);
        drag.start({ clientY: 10, timeStamp: 0 });
        expect(drag.velocity()).toBe(0);
    });

    it('survives two samples sharing a timestamp', () => {
        const drag = createSheetDrag();
        drag.start({ clientY: 0, timeStamp: 12 });
        drag.track({ clientY: 40, timeStamp: 12 });
        expect(Number.isFinite(drag.velocity())).toBe(true);
    });

    it('projects further the faster the release', () => {
        expect(projectOffset(0)).toBe(0);
        expect(projectOffset(1000)).toBeGreaterThan(projectOffset(500));
        // A downward flick projects downward.
        expect(projectOffset(600)).toBeGreaterThan(0);
    });

    it('dismisses a short fast flick and keeps a long slow drag', () => {
        const threshold = 100;
        // Barely moved, but thrown: the projection carries it past the line.
        expect(shouldDismiss(40, 900, threshold)).toBe(true);
        // Dragged most of the way, then stopped: it stays.
        expect(shouldDismiss(90, 0, threshold)).toBe(false);
    });

    it('follows the finger down and resists upward', () => {
        // Downward is one-to-one.
        expect(sheetOffset(120, 600)).toBe(120);
        // Upward is damped, never fully free, and never flips sign.
        const up = sheetOffset(-120, 600);
        expect(up).toBeGreaterThan(-120);
        expect(up).toBeLessThan(0);
    });

    it('resists more the further past the boundary it goes', () => {
        const near = Math.abs(rubberband(-50, 600));
        const far = Math.abs(rubberband(-400, 600));
        // It keeps moving, but each extra pixel of finger buys less travel.
        expect(far).toBeGreaterThan(near);
        expect(far / 400).toBeLessThan(near / 50);
    });

    it('does not divide by zero when the sheet has no measured height', () => {
        expect(sheetOffset(-40, 0)).toBe(-40);
    });
});
