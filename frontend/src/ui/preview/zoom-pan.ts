/**
 * Zoom and pan for one picture.
 *
 * This is the only part of the preview that owns a transform. It was mixed in
 * with the modal's lifecycle, which meant eight module variables of geometry
 * sitting next to the leases, the navigation cursor and the unlock card, and no
 * way to check the arithmetic without opening a modal first. Nothing here knows
 * that a preview exists: it is handed an image element and asked where the
 * picture should sit.
 *
 * Everything below runs on pointer-move and wheel events, so it stays
 * arithmetic on numbers already in hand. The one measurement, the image's
 * bounding rect, is read once per zoom step and never during a pan, because a
 * pan only translates: the picture's painted size cannot have changed between
 * two pixels of finger travel.
 */

/** Past this the picture is a mosaic of its own pixels; there is nothing to see. */
const MAX_ZOOM = 5;

/**
 * Below this a "zoomed" picture is indistinguishable from a fitted one, and
 * leaving it fractionally scaled would keep the swipe-to-close gesture
 * disabled -- the reader would be stuck on a photo that looks perfectly normal.
 */
const ZOOM_SNAP_EPSILON = 1.001;

export interface ZoomPanController {
    /** 1 means fitted. Callers gate swipe and close behaviour on this. */
    readonly scale: number;
    /** Back to fitted, dropping any pan and releasing a pointer mid-drag. */
    reset(): void;
    /**
     * Scale about a screen point, so the pixel under the cursor or between the
     * fingers stays where it is. Anchoring on the image's *current* on-screen
     * center means the result is right whatever the stage's padding or
     * centering does, and whether or not the info panel is pushing it aside.
     */
    zoomAt(clientX: number, clientY: number, factor: number): void;
    /** A pointer went down on the picture: begins a pan if it is zoomed in. */
    pointerDown(event: PointerEvent): void;
    pointerMove(event: PointerEvent): void;
    pointerUp(event: PointerEvent): void;
    /** A second finger landed, so the pan gives its pointer up to the pinch. */
    endPan(): void;
    /**
     * Whether the gesture that just ended actually moved, clearing the flag.
     *
     * The click a drag leaves behind would otherwise close the modal: the
     * reader finishes panning, lets go, and the picture vanishes. The caller
     * asks once, in its click handler, and the answer is consumed so a later
     * genuine click on the backdrop still closes.
     */
    consumePanMoved(): boolean;
}

export function createZoomPanController(getImage: () => HTMLImageElement | null): ZoomPanController {
    // Screen-pixel offsets from the fitted center, not CSS values read back out
    // of the element: the transform string is an output of this state, and
    // parsing it back would make the element the source of truth for a number
    // we already hold.
    let scale = 1;
    let tx = 0;
    let ty = 0;
    let panning = false;
    let panMoved = false;
    let panPointerId = -1;
    let panStartX = 0;
    let panStartY = 0;

    function applyTransform(image: HTMLImageElement): void {
        image.style.transform = `translate(${tx}px, ${ty}px) scale(${scale})`;
        image.style.cursor = scale > 1 ? (panning ? 'grabbing' : 'grab') : 'zoom-in';
    }

    /**
     * The painted size of the picture inside its element box.
     *
     * With `object-fit: contain` the box is larger than the picture on one axis,
     * so clamping against the box would let the reader pan into the empty band
     * beside a portrait photo.
     */
    function paintedSize(image: HTMLImageElement): { w: number; h: number } {
        const cw = image.clientWidth || 0;
        const ch = image.clientHeight || 0;
        const nw = image.naturalWidth || 0;
        const nh = image.naturalHeight || 0;
        if (nw <= 0 || nh <= 0 || cw <= 0 || ch <= 0) return { w: cw, h: ch };
        const fit = Math.min(cw / nw, ch / nh);
        return { w: nw * fit, h: nh * fit };
    }

    /** Keeps the panned picture from drifting past its own painted edges. */
    function clampPan(image: HTMLImageElement): void {
        const { w, h } = paintedSize(image);
        const maxX = Math.max(0, ((scale - 1) * w) / 2);
        const maxY = Math.max(0, ((scale - 1) * h) / 2);
        tx = Math.max(-maxX, Math.min(maxX, tx));
        ty = Math.max(-maxY, Math.min(maxY, ty));
    }

    return {
        get scale() {
            return scale;
        },

        reset(): void {
            scale = 1;
            tx = 0;
            ty = 0;
            panning = false;
            panPointerId = -1;
            const image = getImage();
            if (!image) return;
            image.style.transform = '';
            image.style.cursor = 'zoom-in';
        },

        zoomAt(clientX: number, clientY: number, factor: number): void {
            const image = getImage();
            if (!image) return;
            const next = Math.max(1, Math.min(MAX_ZOOM, scale * factor));
            if (next === scale) return;
            const rect = image.getBoundingClientRect();
            const ax = clientX - (rect.left + rect.width / 2);
            const ay = clientY - (rect.top + rect.height / 2);
            const ratio = next / scale;
            tx += ax * (1 - ratio);
            ty += ay * (1 - ratio);
            scale = next;
            if (scale <= ZOOM_SNAP_EPSILON) {
                scale = 1;
                tx = 0;
                ty = 0;
            }
            clampPan(image);
            applyTransform(image);
        },

        pointerDown(event: PointerEvent): void {
            const image = getImage();
            if (scale <= 1 || !image) return;
            panning = true;
            panMoved = false;
            panPointerId = event.pointerId;
            panStartX = event.clientX - tx;
            panStartY = event.clientY - ty;
            // Capture so a fast drag that leaves the picture keeps being
            // delivered here rather than to whatever is underneath.
            try { image.setPointerCapture(event.pointerId); } catch { /* unsupported or already gone */ }
            image.style.cursor = 'grabbing';
            event.preventDefault();
        },

        pointerMove(event: PointerEvent): void {
            if (!panning || event.pointerId !== panPointerId) return;
            const image = getImage();
            if (!image) return;
            panMoved = true;
            tx = event.clientX - panStartX;
            ty = event.clientY - panStartY;
            clampPan(image);
            applyTransform(image);
        },

        pointerUp(event: PointerEvent): void {
            if (!panning || event.pointerId !== panPointerId) return;
            panning = false;
            panPointerId = -1;
            const image = getImage();
            try { image?.releasePointerCapture(event.pointerId); } catch { /* already released */ }
            if (image) image.style.cursor = scale > 1 ? 'grab' : 'zoom-in';
        },

        endPan(): void {
            if (!panning) return;
            panning = false;
            try { getImage()?.releasePointerCapture(panPointerId); } catch { /* already released */ }
            panPointerId = -1;
        },

        consumePanMoved(): boolean {
            if (!panMoved) return false;
            panMoved = false;
            return true;
        },
    };
}
