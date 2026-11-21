import { readable, type Readable } from 'svelte/store';
import { breadcrumbPath } from '../chrome/breadcrumb-store';

export type NavDirection = 'forward' | 'back' | null;

/**
 * Which way the reader just moved through the folder tree: 'forward' into a
 * folder, 'back' out of one, null once the movement has played. The list sets
 * it as data-nav and CSS does the moving, so prefers-reduced-motion can drop it
 * wholesale and nothing runs on the main thread.
 */
export function navDirection(
    path: Readable<readonly unknown[]> = breadcrumbPath,
    settleMs = 280,
): Readable<NavDirection> {
    return readable<NavDirection>(null, (set) => {
        let lastDepth = -1;
        let timer: ReturnType<typeof setTimeout> | undefined;
        const stop = path.subscribe((crumbs) => {
            const depth = crumbs.length;
            if (lastDepth >= 0 && depth !== lastDepth) {
                set(depth > lastDepth ? 'forward' : 'back');
                clearTimeout(timer);
                // Cleared so a re-render mid-animation does not replay it.
                timer = setTimeout(() => set(null), settleMs);
            }
            lastDepth = depth;
        });
        return () => {
            stop();
            clearTimeout(timer);
        };
    });
}
