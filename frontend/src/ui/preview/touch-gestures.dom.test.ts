import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { bindTouchGestures, type TouchGestureHandlers } from './touch-gestures';

let el: HTMLElement;
let unbind: (() => void) | null = null;

function pointer(type: string, id: number, x: number, y: number, pointerType = 'touch'): void {
    el.dispatchEvent(new PointerEvent(type, {
        pointerId: id,
        pointerType,
        isPrimary: id === 1,
        clientX: x,
        clientY: y,
        bubbles: true,
        cancelable: true,
    }));
}

function bind(handlers: TouchGestureHandlers): TouchGestureHandlers {
    unbind = bindTouchGestures(el, handlers);
    return handlers;
}

beforeEach(() => {
    vi.useFakeTimers();
    el = document.createElement('div');
    document.body.append(el);
});

afterEach(() => {
    unbind?.();
    unbind = null;
    vi.useRealTimers();
    el.remove();
});

describe('bindTouchGestures', () => {
    it('reports a tap at once when double taps are not wanted', () => {
        const handlers = bind({ tap: vi.fn() });
        pointer('pointerdown', 1, 50, 60);
        pointer('pointerup', 1, 52, 61);
        expect(handlers.tap).toHaveBeenCalledWith(52, 61);
    });

    it('waits for a second tap before reporting a single one', () => {
        const handlers = bind({ tap: vi.fn(), doubleTap: vi.fn() });
        pointer('pointerdown', 1, 50, 60);
        pointer('pointerup', 1, 50, 60);
        expect(handlers.tap).not.toHaveBeenCalled();
        vi.advanceTimersByTime(300);
        expect(handlers.tap).toHaveBeenCalledTimes(1);

        pointer('pointerdown', 1, 50, 60);
        pointer('pointerup', 1, 50, 60);
        vi.advanceTimersByTime(120);
        pointer('pointerdown', 1, 54, 62);
        pointer('pointerup', 1, 54, 62);
        vi.advanceTimersByTime(300);
        expect(handlers.doubleTap).toHaveBeenCalledWith(54, 62);
        expect(handlers.tap).toHaveBeenCalledTimes(1);
    });

    it('still pairs a second tap that lands late in the double-tap window', () => {
        const handlers = bind({ tap: vi.fn(), doubleTap: vi.fn() });
        pointer('pointerdown', 1, 50, 60);
        pointer('pointerup', 1, 50, 60);
        vi.advanceTimersByTime(280);
        expect(handlers.tap).not.toHaveBeenCalled();
        pointer('pointerdown', 1, 50, 60);
        pointer('pointerup', 1, 50, 60);
        expect(handlers.doubleTap).toHaveBeenCalledTimes(1);
        vi.advanceTimersByTime(600);
        expect(handlers.tap).not.toHaveBeenCalled();
    });

    it('locks a drag to its dominant axis and reports the release velocity', () => {
        const handlers = bind({ dragStart: vi.fn(() => true), drag: vi.fn(), dragEnd: vi.fn(), tap: vi.fn() });
        pointer('pointerdown', 1, 100, 100);
        pointer('pointermove', 1, 104, 102);
        expect(handlers.dragStart).not.toHaveBeenCalled();
        pointer('pointermove', 1, 130, 104);
        expect(handlers.dragStart).toHaveBeenCalledWith('x');
        expect(handlers.drag).toHaveBeenLastCalledWith(30, 4, 'x');
        pointer('pointermove', 1, 180, 110);
        pointer('pointerup', 1, 180, 110);
        const [dx, dy, axis, velocity] = (handlers.dragEnd as ReturnType<typeof vi.fn>).mock.calls[0];
        expect([dx, dy, axis]).toEqual([80, 10, 'x']);
        expect(velocity).toBeGreaterThanOrEqual(0);
        expect(handlers.tap).not.toHaveBeenCalled();
    });

    it('leaves the pointer alone when the caller refuses the drag', () => {
        const handlers = bind({ dragStart: vi.fn(() => false), drag: vi.fn(), dragEnd: vi.fn() });
        pointer('pointerdown', 1, 100, 100);
        pointer('pointermove', 1, 100, 140);
        pointer('pointermove', 1, 100, 180);
        pointer('pointerup', 1, 100, 180);
        expect(handlers.dragStart).toHaveBeenCalledWith('y');
        expect(handlers.drag).not.toHaveBeenCalled();
        expect(handlers.dragEnd).not.toHaveBeenCalled();
    });

    it('turns two fingers into pinch factors around their midpoint', () => {
        const handlers = bind({ pinchStart: vi.fn(), pinch: vi.fn(), tap: vi.fn() });
        pointer('pointerdown', 1, 100, 100);
        pointer('pointerdown', 2, 200, 100);
        expect(handlers.pinchStart).toHaveBeenCalledTimes(1);
        pointer('pointermove', 2, 300, 100);
        expect(handlers.pinch).toHaveBeenLastCalledWith(2, 200, 100);
        pointer('pointermove', 1, 150, 100);
        expect(handlers.pinch).toHaveBeenLastCalledWith(0.75, 225, 100);
        pointer('pointerup', 2, 300, 100);
        pointer('pointerup', 1, 150, 100);
        expect(handlers.tap).not.toHaveBeenCalled();
    });

    it('ignores the mouse', () => {
        const handlers = bind({ tap: vi.fn() });
        pointer('pointerdown', 1, 10, 10, 'mouse');
        pointer('pointerup', 1, 10, 10, 'mouse');
        expect(handlers.tap).not.toHaveBeenCalled();
    });
});
