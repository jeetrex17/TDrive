import { writable } from 'svelte/store';

/**
 * Where the overlay sits, in viewport coordinates.
 *
 * The overlay is `position: fixed` and covers the file list rather than being a
 * child of it, because the list scrolls and the overlay must not, and because a
 * child would inherit the list's stacking context and be clipped by it. That
 * leaves the box as something only a measurement can answer, which is why it is
 * carried here as four numbers instead of being expressed in CSS.
 */
export interface DropOverlayBox {
    top: number;
    left: number;
    width: number;
    height: number;
}

export interface DropOverlayState {
    /**
     * Whether the overlay is in the layout at all. Separate from `visible`
     * because the fade-out has to run before the element leaves: dropping it
     * the moment the drag ends removes the thing that is meant to be fading.
     */
    present: boolean;
    /** Drives the class the opacity and scale transitions are attached to. */
    visible: boolean;
    /** "Drop to add to Photos" -- the folder or drive the drop will land in. */
    title: string;
    box: DropOverlayBox | null;
}

const closed: DropOverlayState = {
    present: false,
    visible: false,
    // Empty rather than a placeholder because the controller names the drop
    // target before it ever makes the overlay present, so an empty title only
    // belongs to an overlay nobody can see.
    title: '',
    box: null,
};

/**
 * What the drop overlay is doing, written by modules/drop-overlay.ts and read
 * by DropOverlay.svelte.
 *
 * The two used to meet through `getElementById('drop-overlay')`, which made the
 * element's inline style and class list the only record of the overlay's state
 * and gave one element two writers: the controller set `hidden`, the class and
 * four style properties by hand, while Svelte believed it owned the same
 * subtree. Nothing reactive lived in that markup, so the disagreement never
 * surfaced -- but the first reactive thing added to the component would have
 * had its render silently undone by the next dragover.
 */
export const dropOverlayState = writable<DropOverlayState>(closed);

export function setDropOverlayState(next: DropOverlayState): void {
    dropOverlayState.set(next);
}

export function resetDropOverlayState(): void {
    dropOverlayState.set(closed);
}
