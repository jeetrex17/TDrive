import { THEME_TRANSITION_CLASS } from './theme-controller';

const THEME_HIT_TARGET_SELECTOR = '[data-theme-hit-target]';

/**
 * Replays a click that the root View Transition snapshot intercepted.
 *
 * During an active root transition, browsers intentionally remove the live
 * document from hit-testing. A rapid follow-up click is therefore retargeted
 * to <html>, even though the user can still see an Appearance control at that
 * point. We recover only clicks geometrically inside the supplied container,
 * and only onto controls explicitly opted into this behavior.
 */
export function recoverThemeTransitionClick(
    event: MouseEvent,
    container: HTMLElement | null,
): boolean {
    if (!container || !isRootTransitionOverlayClick(event, container.ownerDocument)) return false;
    if (!pointFallsWithin(event.clientX, event.clientY, container.getBoundingClientRect())) return false;

    // Do not let the retargeted <html> click reach outside-click handlers or
    // unrelated page controls after it has been recovered.
    event.preventDefault();
    event.stopImmediatePropagation();

    const target = hitTargetAtPoint(container, event.clientX, event.clientY);
    if (target) dispatchRecoveredClick(target, event);
    return true;
}

function isRootTransitionOverlayClick(event: MouseEvent, targetDocument: Document): boolean {
    const root = targetDocument.documentElement;
    return event.target === root && root.classList.contains(THEME_TRANSITION_CLASS);
}

function hitTargetAtPoint(container: HTMLElement, x: number, y: number): HTMLElement | undefined {
    const targets = container.matches(THEME_HIT_TARGET_SELECTOR)
        ? [container, ...container.querySelectorAll<HTMLElement>(THEME_HIT_TARGET_SELECTOR)]
        : [...container.querySelectorAll<HTMLElement>(THEME_HIT_TARGET_SELECTOR)];

    return targets.find((target) => pointFallsWithin(x, y, target.getBoundingClientRect()));
}

function pointFallsWithin(x: number, y: number, bounds: DOMRect): boolean {
    return bounds.width > 0
        && bounds.height > 0
        && x >= bounds.left
        && x <= bounds.right
        && y >= bounds.top
        && y <= bounds.bottom;
}

function dispatchRecoveredClick(target: HTMLElement, source: MouseEvent): void {
    // Native pointer activation would focus a radio control before its click.
    // The transition overlay prevented that default action, so restore it
    // explicitly to keep roving tabindex and arrow-key navigation aligned.
    target.focus({ preventScroll: true });
    target.dispatchEvent(new MouseEvent('click', {
        bubbles: true,
        cancelable: true,
        composed: true,
        view: source.view,
        detail: source.detail,
        screenX: source.screenX,
        screenY: source.screenY,
        clientX: source.clientX,
        clientY: source.clientY,
        ctrlKey: source.ctrlKey,
        shiftKey: source.shiftKey,
        altKey: source.altKey,
        metaKey: source.metaKey,
        button: source.button,
        buttons: source.buttons,
    }));
}
