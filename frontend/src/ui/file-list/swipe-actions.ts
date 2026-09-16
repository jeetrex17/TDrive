/**
 * Swipe-to-reveal on a phone list row.
 *
 * Two decisions shape everything here.
 *
 * **Only reversible actions ride the swipe.** Move and Share can be undone by
 * doing them again; Delete cannot. A destructive action one careless thumb away
 * from a scrolling list is the single worst pattern in mobile file managers,
 * and the fact that it is the platform default does not make it safe. Delete
 * stays behind the always-visible overflow button, with a confirm.
 *
 * **The swipe never claims a gesture it might not own.** A file list scrolls
 * vertically, the gallery pages horizontally, and iOS reserves the left screen
 * edge for back. So this recogniser stays asleep until the movement is clearly
 * horizontal, refuses anything born in the system's edge zone, and opens only
 * to the trailing side -- leaving the leading side, where back lives,
 * completely untouched.
 */

/** Movement before any direction is committed to. Matches the long-press slop. */
export const SWIPE_SLOP_PX = 10;

/**
 * How much more horizontal than vertical a movement must be before the row
 * takes it. A plain ratio rather than an angle because the failure it prevents
 * is diagonal drift during a fast scroll, and 1.5 keeps the list scrolling
 * unless the thumb really meant sideways.
 */
const HORIZONTAL_BIAS = 1.5;

/**
 * iOS hands any gesture starting within this band to the system back swipe.
 * Anything born here is not ours to interpret, whatever it does next.
 */
export const EDGE_GUARD_PX = 24;

/** Past this speed a release is a throw rather than a placement, in px/s. */
const FLICK_SPEED = 450;

const RUBBERBAND_CONSTANT = 0.55;

export type SwipeClaim = 'row' | 'list' | 'undecided';

/**
 * Who should own a gesture, given how far it has travelled. Called on every
 * move until it stops saying "undecided": the row and the list compete from the
 * first pixel and the loser is dropped once intent is legible, rather than one
 * of them winning by default and feeling sticky.
 */
export function claimGesture(dx: number, dy: number, startX: number): SwipeClaim {
    // Born in the system's edge zone: never ours, at any distance.
    if (startX <= EDGE_GUARD_PX) return 'list';

    const absX = Math.abs(dx);
    const absY = Math.abs(dy);
    if (absX < SWIPE_SLOP_PX && absY < SWIPE_SLOP_PX) return 'undecided';
    // Vertical intent, or an ambiguous diagonal, belongs to the scroller.
    if (absX < absY * HORIZONTAL_BIAS || absX < SWIPE_SLOP_PX) return 'list';
    // Only the trailing direction opens anything; a rightward drag on a closed
    // row has nowhere to go, so it is not worth stealing the scroll for.
    return dx < 0 ? 'row' : 'list';
}

/**
 * Damps movement past the point where the actions are fully shown, so the row
 * slows to a stop like a real object instead of hitting a wall.
 */
export function rubberband(overshoot: number, dimension: number): number {
    if (dimension <= 0) return overshoot;
    return (overshoot * dimension * RUBBERBAND_CONSTANT)
        / (dimension + RUBBERBAND_CONSTANT * Math.abs(overshoot));
}

/**
 * Where the row should sit for a given finger movement. It follows exactly
 * while the actions are being revealed and resists past them; pulling the other
 * way on a closed row does nothing at all, which is how the row says "there is
 * nothing on that side" without a wall.
 */
export function swipeOffset(dx: number, openWidth: number): number {
    if (dx >= 0) return 0;
    const travel = -dx;
    if (travel <= openWidth) return -travel;
    return -(openWidth + rubberband(travel - openWidth, openWidth));
}

/**
 * Whether the row should settle open or closed once the finger lifts.
 *
 * Decided on where the gesture was heading, not where it stopped: a quick flick
 * that has clearly left should open even though it covered little ground, and a
 * slow drag that halted halfway should stay where the user parked it. Position
 * only decides the cases where there was no real speed either way.
 */
export function shouldOpen(offset: number, velocity: number, openWidth: number): boolean {
    if (openWidth <= 0) return false;
    // A decisive throw wins outright, in whichever direction it went.
    if (velocity <= -FLICK_SPEED) return true;
    if (velocity >= FLICK_SPEED) return false;
    return -offset >= openWidth / 2;
}

/**
 * Binds swipe-to-reveal to every row under `host` matching `selector`,
 * delegated so a virtualised list can recycle rows freely.
 *
 * At most one row is open at a time. Opening a second closes the first, and so
 * do scrolling and a press anywhere else: an open row is a mode, and a mode
 * that survives the user moving on is a trap.
 */
export interface SwipeRowsOptions {
    /** Width of the revealed action strip, measured from the row as it opens. */
    openWidth: (row: HTMLElement) => number;
    /** Called when a row settles open or closed, so the view can mark state. */
    onSettle: (row: HTMLElement, open: boolean) => void;
}

export function bindSwipeActions(host: HTMLElement, selector: string, options: SwipeRowsOptions): () => void {
    let row: HTMLElement | null = null;
    let openRow: HTMLElement | null = null;
    let pointerId = -1;
    let startX = 0;
    let startY = 0;
    let width = 0;
    let claimed = false;
    // Samples rather than the last event alone: one sample is noisy, and a
    // finger that paused before lifting would otherwise read as a flick.
    let samples: Array<{ x: number; t: number }> = [];

    function place(target: HTMLElement, offset: number, animate: boolean): void {
        // No transition while the finger is down, so the row tracks 1:1 rather
        // than easing along behind it.
        target.style.transition = animate ? 'transform 220ms cubic-bezier(0.2, 0, 0, 1)' : 'none';
        target.style.transform = offset === 0 ? '' : `translate3d(${offset}px, 0, 0)`;
    }

    function settle(target: HTMLElement, open: boolean): void {
        place(target, open ? -width : 0, true);
        if (open) openRow = target;
        else if (openRow === target) openRow = null;
        options.onSettle(target, open);
    }

    /** Closes whatever is open. Safe to call when nothing is. */
    function closeOpen(): void {
        if (!openRow) return;
        const target = openRow;
        openRow = null;
        width = options.openWidth(target);
        place(target, 0, true);
        options.onSettle(target, false);
    }

    function lastDx(): number {
        return samples.length ? samples[samples.length - 1].x - startX : 0;
    }

    function releaseVelocity(): number {
        if (samples.length < 2) return 0;
        const first = samples[0];
        const last = samples[samples.length - 1];
        const elapsed = last.t - first.t;
        if (elapsed <= 0) return 0;
        return ((last.x - first.x) / elapsed) * 1000;
    }

    function release(): void {
        if (row && claimed) {
            settle(row, shouldOpen(swipeOffset(lastDx(), width), releaseVelocity(), width));
        }
        row = null;
        pointerId = -1;
        claimed = false;
        samples = [];
    }

    const onPointerDown = (event: PointerEvent): void => {
        if (event.pointerType === 'mouse' || !event.isPrimary) return;
        const origin = event.target instanceof Element ? event.target : null;
        const target = origin?.closest<HTMLElement>(selector);
        // A press anywhere but the open row closes it, so the mode never
        // outlives the user's attention.
        if (openRow && target !== openRow) closeOpen();
        if (!target) return;
        row = target;
        pointerId = event.pointerId;
        startX = event.clientX;
        startY = event.clientY;
        claimed = false;
        samples = [{ x: event.clientX, t: event.timeStamp }];
    };

    const onPointerMove = (event: PointerEvent): void => {
        if (!row || event.pointerId !== pointerId) return;
        samples.push({ x: event.clientX, t: event.timeStamp });
        if (samples.length > 5) samples.shift();

        const dx = event.clientX - startX;
        const dy = event.clientY - startY;

        if (!claimed) {
            const claim = claimGesture(dx, dy, startX);
            if (claim === 'undecided') return;
            // Losing the race drops the row out of the gesture entirely rather
            // than leaving it half-listening.
            if (claim === 'list') { row = null; pointerId = -1; return; }
            claimed = true;
            width = options.openWidth(row);
            // Capture so tracking survives the finger leaving the row's bounds.
            row.setPointerCapture(event.pointerId);
        }

        place(row, swipeOffset(dx, width), false);
        event.preventDefault();
    };

    const onPointerUp = (event: PointerEvent): void => {
        if (!row || event.pointerId !== pointerId) return;
        release();
    };

    const onScroll = (): void => closeOpen();

    host.addEventListener('pointerdown', onPointerDown);
    host.addEventListener('pointermove', onPointerMove, { passive: false });
    host.addEventListener('pointerup', onPointerUp);
    host.addEventListener('pointercancel', onPointerUp);
    host.addEventListener('scroll', onScroll, { passive: true });

    return () => {
        closeOpen();
        host.removeEventListener('pointerdown', onPointerDown);
        host.removeEventListener('pointermove', onPointerMove);
        host.removeEventListener('pointerup', onPointerUp);
        host.removeEventListener('pointercancel', onPointerUp);
        host.removeEventListener('scroll', onScroll);
    };
}
