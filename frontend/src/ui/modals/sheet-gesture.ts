/**
 * The physics behind dragging a bottom sheet, shared by every sheet in the app
 * so they all behave the same way under a thumb.
 *
 * Two ideas do most of the work. A sheet is dismissed on where the gesture was
 * heading, not on where the finger happened to stop, so a short fast flick
 * throws it away while a long slow drag that halted keeps it. And a drag past a
 * boundary resists progressively instead of stopping dead, because a hard stop
 * reads as frozen while resistance reads as "responsive, but there is nothing
 * more here".
 */

/** How quickly a released sheet gives up its speed. Matches scroll deceleration. */
const DECELERATION = 0.998;

/** Samples kept for the velocity estimate. */
const SAMPLE_LIMIT = 5;

/** Past this speed the release is a flick rather than a placement, in px/s. */
export const FLICK_SPEED = 600;

const RUBBERBAND_CONSTANT = 0.55;

export interface DragSample {
    y: number;
    t: number;
}

export function createSheetDrag() {
    let samples: DragSample[] = [];

    return {
        start(event: { clientY: number; timeStamp: number }): void {
            samples = [{ y: event.clientY, t: event.timeStamp }];
        },
        track(event: { clientY: number; timeStamp: number }): void {
            samples.push({ y: event.clientY, t: event.timeStamp });
            if (samples.length > SAMPLE_LIMIT) samples.shift();
        },
        reset(): void {
            samples = [];
        },
        /**
         * Release speed in px/s, positive downward. Measured across the recent
         * samples rather than the last event alone: one sample is noisy, and a
         * finger that paused before lifting would otherwise read as a flick.
         */
        velocity(): number {
            if (samples.length < 2) return 0;
            const first = samples[0];
            const last = samples[samples.length - 1];
            const elapsed = last.t - first.t;
            if (elapsed <= 0) return 0;
            return ((last.y - first.y) / elapsed) * 1000;
        },
    };
}

/**
 * How much further a sheet travelling at this speed would coast before
 * stopping. Added to the current offset, it gives the point the gesture was
 * aiming at, which is what the dismiss decision should be made against.
 */
export function projectOffset(velocity: number): number {
    return (velocity / 1000) * DECELERATION / (1 - DECELERATION);
}

/**
 * Damps movement past a boundary. The further beyond it the finger goes, the
 * less the sheet follows, so it slows to a stop like a real object instead of
 * hitting a wall.
 */
export function rubberband(overshoot: number, dimension: number): number {
    if (dimension <= 0) return overshoot;
    return (overshoot * dimension * RUBBERBAND_CONSTANT)
        / (dimension + RUBBERBAND_CONSTANT * Math.abs(overshoot));
}

/**
 * Turns a raw finger delta into the offset the sheet should sit at: it follows
 * exactly when dragged down, and resists when dragged up past its open
 * position, where there is nowhere left to go.
 */
export function sheetOffset(rawDelta: number, sheetHeight: number): number {
    return rawDelta >= 0 ? rawDelta : rubberband(rawDelta, sheetHeight);
}

/** True when the gesture, carried forward, would take the sheet past the point of no return. */
export function shouldDismiss(offset: number, velocity: number, threshold: number): boolean {
    return offset + projectOffset(velocity) > threshold;
}

/**
 * Detents: the heights a sheet is allowed to rest at.
 *
 * Most sheets want exactly one, and giving those a second one is the "sheet
 * soup" failure -- a surface that can be at two heights makes the reader decide
 * something before they can read it. So detents are opt-in, and a sheet with a
 * single detent behaves exactly as it did before this existed. They earn their
 * place only where the sheet genuinely has two useful sizes: a compact list
 * that can grow to a long one, a transfer queue that can be glanced at or
 * worked through.
 */

/**
 * Where a released sheet should settle, given the height it is at now.
 *
 * The decision is made against where the gesture was *heading*, not where the
 * finger stopped, for the same reason dismissal is: a flick that has clearly
 * left for the next detent should not snap back because the thumb ran out of
 * screen.
 *
 * `detents` are heights in px and need not be sorted. Returns the height to
 * settle at, or null when the gesture was heading past the dismiss line -- in
 * which case the sheet should close rather than snap to its smallest detent.
 */
export function settleToDetent(
    currentHeight: number,
    velocity: number,
    detents: readonly number[],
    dismissBelow: number,
): number | null {
    if (detents.length === 0) return null;

    // Velocity is positive downward and downward shrinks the sheet, so the
    // projected coast is subtracted from the height.
    const projected = currentHeight - projectOffset(velocity);
    if (projected < dismissBelow) return null;

    let best = detents[0];
    let bestGap = Math.abs(projected - best);
    for (const detent of detents) {
        const gap = Math.abs(projected - detent);
        // Ties go to the smaller detent: when a release lands exactly between
        // two, collapsing shows the content behind, which is recoverable with
        // one drag. Expanding hides it, which is not as obviously undoable.
        if (gap < bestGap || (gap === bestGap && detent < best)) {
            best = detent;
            bestGap = gap;
        }
    }
    return best;
}

/**
 * The detent a scroll-to-sheet handoff should move to, or null to let the
 * inner content keep the gesture.
 *
 * The handoff rule: dragging up while the content is already at its top may
 * grow the sheet, because there is nothing above to scroll to. Dragging down
 * scrolls the content first and only reaches the sheet once the content is at
 * its top -- otherwise a sheet would fly away under a thumb that was only
 * trying to read the list inside it.
 */
export function handoffTarget(
    direction: 'up' | 'down',
    contentAtTop: boolean,
    currentHeight: number,
    detents: readonly number[],
): number | null {
    if (!contentAtTop) return null;
    const sorted = [...detents].sort((a, b) => a - b);
    if (direction === 'up') return sorted.find((detent) => detent > currentHeight) ?? null;
    return [...sorted].reverse().find((detent) => detent < currentHeight) ?? null;
}
