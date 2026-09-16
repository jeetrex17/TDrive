// A small touch recogniser for the viewers: tap, double tap, one-finger drag
// on a locked axis and two-finger pinch. It never captures pointers, so a
// surface that already pans a zoomed picture keeps doing so; it only reports
// what the fingers are doing and lets the caller decide.

export type DragAxis = 'x' | 'y';

export interface TouchGestureHandlers {
    tap?(x: number, y: number): void;
    doubleTap?(x: number, y: number): void;
    /** Return false to leave this pointer to whoever else is listening. */
    dragStart?(axis: DragAxis): boolean;
    drag?(dx: number, dy: number, axis: DragAxis): void;
    /** velocity is px per ms along the locked axis. */
    dragEnd?(dx: number, dy: number, axis: DragAxis, velocity: number): void;
    pinchStart?(): void;
    /** factor is the change since the previous pinch event, centred on x, y. */
    pinch?(factor: number, x: number, y: number): void;
}

interface TrackedPointer {
    x: number;
    y: number;
    startX: number;
    startY: number;
    startTime: number;
}

const DRAG_SLOP_PX = 8;
const TAP_MAX_MS = 500;
const DOUBLE_TAP_MS = 300;
const DOUBLE_TAP_SLOP_PX = 32;
// A single tap waits for a second one only when the caller cares about double
// taps, and it waits out the whole pairing window: committing earlier dropped
// every double tap in the tail of that window and fired two single taps
// instead (two chrome toggles rather than a zoom).
const SINGLE_TAP_DELAY_MS = DOUBLE_TAP_MS;

type Mode = 'idle' | 'pending' | 'drag' | 'pinch' | 'dead';

export function bindTouchGestures(el: HTMLElement, handlers: TouchGestureHandlers): () => void {
    const pointers = new Map<number, TrackedPointer>();
    let mode: Mode = 'idle';
    let axis: DragAxis = 'x';
    let lastDistance = 0;
    let lastTap: { x: number; y: number; time: number } | null = null;
    let tapTimer = 0;

    const clearTapTimer = () => {
        window.clearTimeout(tapTimer);
        tapTimer = 0;
    };
    const distance = () => {
        const [a, b] = Array.from(pointers.values());
        return a && b ? Math.hypot(b.x - a.x, b.y - a.y) : 0;
    };
    const midpoint = () => {
        const [a, b] = Array.from(pointers.values());
        return a && b ? { x: (a.x + b.x) / 2, y: (a.y + b.y) / 2 } : { x: 0, y: 0 };
    };
    const endDrag = (pointer: TrackedPointer, time: number) => {
        const dx = pointer.x - pointer.startX;
        const dy = pointer.y - pointer.startY;
        const travelled = axis === 'x' ? Math.abs(dx) : Math.abs(dy);
        const velocity = travelled / Math.max(1, time - pointer.startTime);
        handlers.dragEnd?.(dx, dy, axis, velocity);
    };

    const onPointerDown = (event: PointerEvent) => {
        if (event.pointerType === 'mouse') return;
        pointers.set(event.pointerId, {
            x: event.clientX,
            y: event.clientY,
            startX: event.clientX,
            startY: event.clientY,
            startTime: event.timeStamp,
        });
        if (pointers.size === 1) {
            mode = 'pending';
            return;
        }
        if (pointers.size === 2) {
            clearTapTimer();
            lastTap = null;
            if (mode === 'drag') handlers.dragEnd?.(0, 0, axis, 0);
            mode = 'pinch';
            lastDistance = distance();
            handlers.pinchStart?.();
            return;
        }
        mode = 'dead';
    };

    const onPointerMove = (event: PointerEvent) => {
        const pointer = pointers.get(event.pointerId);
        if (!pointer) return;
        pointer.x = event.clientX;
        pointer.y = event.clientY;
        if (mode === 'pinch') {
            const next = distance();
            if (lastDistance > 0 && next > 0) {
                const { x, y } = midpoint();
                handlers.pinch?.(next / lastDistance, x, y);
            }
            lastDistance = next;
            return;
        }
        const dx = pointer.x - pointer.startX;
        const dy = pointer.y - pointer.startY;
        if (mode === 'pending') {
            if (Math.hypot(dx, dy) <= DRAG_SLOP_PX) return;
            axis = Math.abs(dx) >= Math.abs(dy) ? 'x' : 'y';
            if (handlers.dragStart?.(axis) === false) {
                mode = 'dead';
                return;
            }
            mode = 'drag';
        }
        if (mode === 'drag') handlers.drag?.(dx, dy, axis);
    };

    const onPointerEnd = (event: PointerEvent) => {
        const pointer = pointers.get(event.pointerId);
        if (!pointer) return;
        pointer.x = event.clientX;
        pointer.y = event.clientY;
        pointers.delete(event.pointerId);
        if (mode === 'drag') {
            endDrag(pointer, event.timeStamp);
            mode = pointers.size ? 'dead' : 'idle';
            return;
        }
        if (mode === 'pending' && event.type === 'pointerup' && event.timeStamp - pointer.startTime <= TAP_MAX_MS) {
            const x = pointer.x;
            const y = pointer.y;
            const now = event.timeStamp;
            if (lastTap && now - lastTap.time <= DOUBLE_TAP_MS && Math.hypot(x - lastTap.x, y - lastTap.y) <= DOUBLE_TAP_SLOP_PX) {
                clearTapTimer();
                lastTap = null;
                handlers.doubleTap?.(x, y);
            } else {
                lastTap = { x, y, time: now };
                if (handlers.doubleTap) {
                    clearTapTimer();
                    tapTimer = window.setTimeout(() => {
                        tapTimer = 0;
                        // Reported as a single tap, so it can no longer pair.
                        lastTap = null;
                        handlers.tap?.(x, y);
                    }, SINGLE_TAP_DELAY_MS);
                } else {
                    handlers.tap?.(x, y);
                }
            }
        }
        // A finger left mid-pinch: the one still down must not start a drag.
        mode = pointers.size === 0 ? 'idle' : 'dead';
    };

    el.addEventListener('pointerdown', onPointerDown);
    el.addEventListener('pointermove', onPointerMove);
    el.addEventListener('pointerup', onPointerEnd);
    el.addEventListener('pointercancel', onPointerEnd);
    return () => {
        clearTapTimer();
        pointers.clear();
        el.removeEventListener('pointerdown', onPointerDown);
        el.removeEventListener('pointermove', onPointerMove);
        el.removeEventListener('pointerup', onPointerEnd);
        el.removeEventListener('pointercancel', onPointerEnd);
    };
}
