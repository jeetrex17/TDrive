/**
 * How the phone shell should scroll, given what the person has asked for.
 *
 * A programmatic smooth scroll is the one piece of motion a stylesheet cannot
 * turn off: `behavior` is an argument to the call, not a property a media query
 * can answer for. So every scroll the shell starts asks here first, and under
 * Reduce Motion the page simply arrives where it was going.
 */
export function scrollBehavior(): ScrollBehavior {
    if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return 'auto';
    return window.matchMedia('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth';
}
