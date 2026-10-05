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

/** Controls that must swallow a press rather than let it begin a drag. */
const INTERACTIVE_IN_GRAB = 'button, a, input, select, textarea, [role="button"], [role="menuitem"]';

export interface SheetDragController {
    onPointerDown: (event: PointerEvent) => void;
    onPointerMove: (event: PointerEvent) => void;
    onPointerUp: (event: PointerEvent) => void;
    /** True while a drag is in progress, for callers that mirror it into state. */
    isDragging: () => boolean;
}

export interface SheetDragOptions {
    /** The sheet element the gesture translates. */
    sheet: () => HTMLElement | null;
    /** Dismiss the sheet through its own close path. */
    dismiss: () => void;
    /** Return the sheet to rest. Receives release speed in px/s. */
    settle: (velocity: number) => void;
    /** Each move reports the clamped offset and the sheet height (for a scrim). */
    track?: (offset: number, height: number) => void;
    /** Ready the element for a 1:1 drag; defaults to clearing transition/animation. */
    grab?: (sheet: HTMLElement) => void;
    /** Dismiss threshold in px for the sheet height; defaults to max(88, 28%). */
    threshold?: (height: number) => number;
}

/**
 * The pointer wiring every bottom sheet shares: follow the finger down, resist
 * past the open position, and on release judge where the gesture was heading.
 * The three sheet hosts differ only in how they settle and dismiss, which they
 * pass in, so the drag itself has one definition rather than three copies.
 *
 * A press that lands on an interactive control does not start a drag, so a
 * header can be a grab area and still carry working buttons.
 */
export function createSheetDragController(options: SheetDragOptions): SheetDragController {
    const drag = createSheetDrag();
    let dragging = false;
    let startY = 0;
    let delta = 0;

    return {
        isDragging: () => dragging,
        onPointerDown(event: PointerEvent): void {
            const sheet = options.sheet();
            if (!sheet) return;
            if (event.target instanceof Element && event.target.closest(INTERACTIVE_IN_GRAB)) return;
            dragging = true;
            startY = event.clientY;
            delta = 0;
            drag.start(event);
            if (options.grab) {
                options.grab(sheet);
            } else {
                sheet.style.transition = '';
                sheet.style.animation = 'none';
            }
            (event.currentTarget as HTMLElement | null)?.setPointerCapture?.(event.pointerId);
        },
        onPointerMove(event: PointerEvent): void {
            const sheet = options.sheet();
            if (!dragging || !sheet) return;
            delta = sheetOffset(event.clientY - startY, sheet.offsetHeight);
            drag.track(event);
            sheet.style.transform = `translateY(${delta}px)`;
            options.track?.(delta, sheet.offsetHeight || 1);
        },
        onPointerUp(): void {
            const sheet = options.sheet();
            if (!dragging || !sheet) return;
            dragging = false;
            const velocity = drag.velocity();
            const height = sheet.offsetHeight || 0;
            const threshold = options.threshold
                ? options.threshold(height)
                : Math.max(88, height * 0.28);
            const dismissed = shouldDismiss(delta, velocity, threshold);
            delta = 0;
            drag.reset();
            if (dismissed) options.dismiss();
            else options.settle(velocity);
        },
    };
}
