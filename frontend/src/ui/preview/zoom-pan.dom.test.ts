import { beforeEach, describe, expect, it } from 'vitest';
import { createZoomPanController, type ZoomPanController } from './zoom-pan';

let image: HTMLImageElement;
let zoomPan: ZoomPanController;

// happy-dom lays nothing out, so the picture's geometry is stated outright:
// a 400x300 box painting a 400x300 photo, centred on a 400x300 screen.
function measure(image: HTMLImageElement, { width = 400, height = 300 } = {}): void {
    Object.defineProperty(image, 'clientWidth', { value: width, configurable: true });
    Object.defineProperty(image, 'clientHeight', { value: height, configurable: true });
    Object.defineProperty(image, 'naturalWidth', { value: width, configurable: true });
    Object.defineProperty(image, 'naturalHeight', { value: height, configurable: true });
    image.getBoundingClientRect = () => ({
        left: 0, top: 0, right: width, bottom: height, width, height, x: 0, y: 0,
        toJSON: () => ({}),
    });
}

function pointer(type: string, id: number, x: number, y: number): PointerEvent {
    const event = new PointerEvent(type, {
        pointerId: id, clientX: x, clientY: y, bubbles: true, cancelable: true,
    });
    return event;
}

beforeEach(() => {
    document.body.innerHTML = '<img id="preview-image" alt="">';
    image = document.getElementById('preview-image') as HTMLImageElement;
    image.setPointerCapture = () => {};
    image.releasePointerCapture = () => {};
    measure(image);
    zoomPan = createZoomPanController(() => image);
});

describe('zooming a preview', () => {
    it('starts fitted, with nothing written to the picture', () => {
        expect(zoomPan.scale).toBe(1);
        expect(image.style.transform).toBe('');
    });

    it('never zooms out past a fitted picture', () => {
        zoomPan.zoomAt(200, 150, 0.2);
        expect(zoomPan.scale).toBe(1);
    });

    it('stops magnifying at five times, however hard the reader pinches', () => {
        for (let i = 0; i < 20; i += 1) zoomPan.zoomAt(200, 150, 2);
        expect(zoomPan.scale).toBe(5);
    });

    it('keeps the pixel under the cursor where it is', () => {
        // Zooming 2x about the picture's own centre needs no translation at
        // all: the anchor and the centre are the same point.
        zoomPan.zoomAt(200, 150, 2);
        expect(zoomPan.scale).toBe(2);
        expect(image.style.transform).toBe('translate(0px, 0px) scale(2)');

        // Anchored on the left edge instead, the picture has to slide right by
        // the distance that edge would otherwise have travelled outwards.
        zoomPan.reset();
        zoomPan.zoomAt(0, 150, 2);
        expect(image.style.transform).toBe('translate(200px, 0px) scale(2)');
    });

    it('snaps back to fitted rather than leaving a picture fractionally scaled', () => {
        zoomPan.zoomAt(200, 150, 2);
        zoomPan.zoomAt(200, 150, 1 / 1.9995);
        expect(zoomPan.scale).toBe(1);
        expect(image.style.transform).toBe('translate(0px, 0px) scale(1)');
    });

    it('offers to zoom in when fitted and to grab when magnified', () => {
        zoomPan.zoomAt(200, 150, 2);
        expect(image.style.cursor).toBe('grab');
        zoomPan.reset();
        expect(image.style.cursor).toBe('zoom-in');
    });

    it('does nothing at all when the picture has gone', () => {
        const detached = createZoomPanController(() => null);
        expect(() => {
            detached.zoomAt(10, 10, 2);
            detached.reset();
            detached.pointerDown(pointer('pointerdown', 1, 0, 0));
        }).not.toThrow();
        expect(detached.scale).toBe(1);
    });
});

describe('panning a magnified preview', () => {
    beforeEach(() => {
        zoomPan.zoomAt(200, 150, 2);
    });

    it('ignores a drag on a picture that is only fitted', () => {
        zoomPan.reset();
        zoomPan.pointerDown(pointer('pointerdown', 1, 100, 100));
        zoomPan.pointerMove(pointer('pointermove', 1, 180, 100));
        expect(image.style.transform).toBe('');
    });

    it('follows the pointer that started the drag and no other', () => {
        zoomPan.pointerDown(pointer('pointerdown', 1, 100, 100));
        zoomPan.pointerMove(pointer('pointermove', 2, 300, 100));
        expect(image.style.transform).toBe('translate(0px, 0px) scale(2)');
        zoomPan.pointerMove(pointer('pointermove', 1, 150, 100));
        expect(image.style.transform).toBe('translate(50px, 0px) scale(2)');
    });

    it("will not let the reader pan past the picture's own painted edge", () => {
        zoomPan.pointerDown(pointer('pointerdown', 1, 100, 100));
        // At 2x a 400px-wide photo paints 800px, so 200px hangs off each side
        // and that is as far as it can travel; a 4000px throw stops there too.
        zoomPan.pointerMove(pointer('pointermove', 1, 4100, 100));
        expect(image.style.transform).toBe('translate(200px, 0px) scale(2)');
    });

    it('reports the drag once, so the click it leaves behind does not close the preview', () => {
        zoomPan.pointerDown(pointer('pointerdown', 1, 100, 100));
        zoomPan.pointerMove(pointer('pointermove', 1, 150, 100));
        zoomPan.pointerUp(pointer('pointerup', 1, 150, 100));
        expect(zoomPan.consumePanMoved()).toBe(true);
        expect(zoomPan.consumePanMoved()).toBe(false);
    });

    it('reports nothing for a press that never moved, which is a real click', () => {
        zoomPan.pointerDown(pointer('pointerdown', 1, 100, 100));
        zoomPan.pointerUp(pointer('pointerup', 1, 100, 100));
        expect(zoomPan.consumePanMoved()).toBe(false);
    });

    it('lets go of its pointer when a second finger turns the drag into a pinch', () => {
        zoomPan.pointerDown(pointer('pointerdown', 1, 100, 100));
        zoomPan.endPan();
        zoomPan.pointerMove(pointer('pointermove', 1, 300, 100));
        expect(image.style.transform).toBe('translate(0px, 0px) scale(2)');
    });

    it('drops the pan when the picture is reset under it', () => {
        zoomPan.pointerDown(pointer('pointerdown', 1, 100, 100));
        zoomPan.reset();
        zoomPan.pointerMove(pointer('pointermove', 1, 300, 100));
        expect(image.style.transform).toBe('');
    });
});
