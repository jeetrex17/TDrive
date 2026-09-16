/**
 * What the person has asked for, where a stylesheet cannot answer it.
 *
 * A programmatic smooth scroll is the one piece of motion a stylesheet cannot
 * turn off: `behavior` is an argument to the call, not a property a media query
 * can answer for. So every scroll the shell starts asks here first, and under
 * Reduce Motion the page simply arrives where it was going.
 */
export function prefersReducedMotion(): boolean {
    if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return false;
    return window.matchMedia('(prefers-reduced-motion: reduce)').matches;
}

export function scrollBehavior(): ScrollBehavior {
    return prefersReducedMotion() ? 'auto' : 'smooth';
}
