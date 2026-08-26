/**
 * Tests the immutable event path before falling back to live DOM containment.
 * Reactive updates can detach a click target before a delegated document
 * handler runs, while composedPath() still describes where the click began.
 */
export function eventOccurredWithin(event: Event, container: Node | null): boolean {
    if (!container) return false;

    try {
        if (typeof event.composedPath === 'function' && event.composedPath().includes(container)) {
            return true;
        }
    } catch {
        // A closing webview can invalidate the composed path; use the target.
    }

    const target = event.target;
    return Boolean(target && 'nodeType' in target && container.contains(target as Node));
}
