// One feel for the photo, video and document viewers: their controls fade on
// the same beat and a swipe down closes each at the same distance, so moving
// between them never changes how the gesture or the chrome behaves.

/** How long the controls stay after the last touch or pointer move. */
export const VIEWER_CHROME_HIDE_MS = 2500;

/** A swipe down past this distance closes the viewer. */
export const VIEWER_DISMISS_PX = 110;

/** A shorter swipe down still closes it when thrown at least this fast (px/ms). */
export const VIEWER_DISMISS_VELOCITY = 0.6;

/** The least distance a thrown swipe has to cover. */
export const VIEWER_DISMISS_FLICK_PX = 28;

/** Whether a vertical swipe of dy px at this speed should close the viewer. */
export function isDismissSwipe(dy: number, velocity: number): boolean {
    return dy > VIEWER_DISMISS_PX || (dy > VIEWER_DISMISS_FLICK_PX && velocity > VIEWER_DISMISS_VELOCITY);
}
