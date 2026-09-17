/**
 * Putting a bottom-edge surface away with a thumb.
 *
 * A toast arrives from the bottom of the screen, so the gesture that dismisses
 * it is the one that pushes it back down -- the same direction, and the same
 * physics, a bottom sheet uses. The maths lives in ui/modals/sheet-gesture and
 * is imported rather than restated: a second set of constants would drift from
 * the first the moment either was tuned, and then two surfaces would answer the
 * same thumb differently.
 *
 * The action owns the whole touch interaction, tap included. A tap is a drag
 * that never moved, so deciding both here keeps one gesture to one decision:
 * wired separately, a tap would spring the surface back and dismiss it at once.
 *
 * A pointer that cannot flick -- a mouse, a trackpad -- is left alone. It has a
 * close button, and swallowing its drags would take text selection with them.
 */

import { createSheetDrag, sheetOffset, shouldDismiss } from '../modals/sheet-gesture';
import { prefersReducedMotion } from '../mobile/motion';

/** Movement under this is a tap, not a drag. Roughly a fingertip's wobble. */
const TAP_SLOP_PX = 8;

/** Matches --motion-med, so a swiped toast and an expired one leave alike. */
export const DISMISS_EXIT_MS = 180;

/** Shortest travel that can dismiss, for surfaces too short for a ratio to mean much. */
const MIN_THRESHOLD_PX = 28;

/** How much of the surface's height must be crossed on a slow, deliberate drag. */
const THRESHOLD_RATIO = 0.4;

/** Opacity given up at the moment of dismissal, so the fade tracks the travel. */
const FADE_DEPTH = 0.65;

export interface SwipeDismissOptions {
    /** Called once the surface has left, by tap or by flick. */
    onDismiss: () => void;
}

/**
 * Whether the press landed on something with its own job -- a Retry button, a
 * link. Those keep their tap; only the surface around them dismisses.
 */
function onInteractive(target: EventTarget | null): boolean {
    return Boolean((target as HTMLElement | null)?.closest?.('[data-toast-interactive]'));
}

export function swipeDismiss(node: HTMLElement, options: SwipeDismissOptions) {
    let current = options;
    const drag = createSheetDrag();
    let dragging = false;
    let startY = 0;
    let offset = 0;

    /** Follows the finger, fading as it goes so the travel reads as departure. */
    function paint(): void {
        const height = node.offsetHeight || 1;
        const progress = Math.min(1, Math.max(0, offset / height));
        node.style.transform = `translateY(${offset}px)`;
        node.style.opacity = String(1 - progress * FADE_DEPTH);
    }

    function clearInlineMotion(): void {
        node.style.transition = '';
        node.style.transform = '';
        node.style.opacity = '';
    }

    function springBack(): void {
        node.style.transition = `transform ${DISMISS_EXIT_MS}ms var(--ease-enter), opacity ${DISMISS_EXIT_MS}ms linear`;
        node.style.transform = 'translateY(0)';
        node.style.opacity = '1';
        window.setTimeout(clearInlineMotion, DISMISS_EXIT_MS);
    }

    /**
     * Carries the surface off the bottom edge and hands over once it is gone.
     * Under Reduce Motion it simply goes, which is what "reduce" asks for.
     */
    function leave(): void {
        if (prefersReducedMotion()) {
            current.onDismiss();
            return;
        }
        const remaining = (node.offsetHeight || 0) + 24 - offset;
        node.style.transition = `transform ${DISMISS_EXIT_MS}ms var(--ease-standard), opacity ${DISMISS_EXIT_MS}ms linear`;
        node.style.transform = `translateY(${offset + Math.max(0, remaining)}px)`;
        node.style.opacity = '0';
        window.setTimeout(() => current.onDismiss(), DISMISS_EXIT_MS);
    }

    function onPointerDown(event: PointerEvent): void {
        if (event.pointerType !== 'touch' || onInteractive(event.target)) return;
        dragging = true;
        startY = event.clientY;
        offset = 0;
        drag.start(event);
        node.style.transition = '';
        // A CSS animation outranks an inline style for as long as it is
        // running, so the arrival has to be called off or the first moments of
        // a drag on a just-appeared toast would go nowhere. It is not restored:
        // a surface the user has taken hold of should not play its entrance
        // again afterwards.
        node.style.animation = 'none';
        node.setPointerCapture?.(event.pointerId);
    }

    function onPointerMove(event: PointerEvent): void {
        if (!dragging) return;
        // Follows the finger down; resists upward, where there is nowhere to go.
        offset = sheetOffset(event.clientY - startY, node.offsetHeight || 0);
        drag.track(event);
        paint();
    }

    function onPointerUp(event: PointerEvent): void {
        if (!dragging) return;
        dragging = false;
        const travelled = Math.abs(event.clientY - startY);
        const velocity = drag.velocity();
        drag.reset();

        // A press that never moved is a tap, and a notice that has been read is
        // in the way: it goes, without asking the thumb to find a small close
        // button sitting above the tab bar.
        if (travelled < TAP_SLOP_PX) {
            offset = 0;
            clearInlineMotion();
            current.onDismiss();
            return;
        }

        const threshold = Math.max(MIN_THRESHOLD_PX, (node.offsetHeight || 0) * THRESHOLD_RATIO);
        // Judged on where the gesture was heading, not where the finger stopped.
        if (shouldDismiss(offset, velocity, threshold)) leave();
        else springBack();
        offset = 0;
    }

    function onPointerCancel(): void {
        if (!dragging) return;
        dragging = false;
        drag.reset();
        offset = 0;
        springBack();
    }

    node.addEventListener('pointerdown', onPointerDown);
    node.addEventListener('pointermove', onPointerMove);
    node.addEventListener('pointerup', onPointerUp);
    node.addEventListener('pointercancel', onPointerCancel);

    return {
        update(next: SwipeDismissOptions) {
            current = next;
        },
        destroy() {
            node.removeEventListener('pointerdown', onPointerDown);
            node.removeEventListener('pointermove', onPointerMove);
            node.removeEventListener('pointerup', onPointerUp);
            node.removeEventListener('pointercancel', onPointerCancel);
        },
    };
}
