/**
 * Rows the app is working on right now.
 *
 * A destructive command runs item by item and only refreshes the list when the
 * whole batch is finished, so between the confirmation and the refresh the list
 * sits there looking untouched. That gap used to be covered by a spinner toast
 * parked over the content -- a notice about rows the user was already looking
 * at, telling them something the rows themselves should say.
 *
 * They say it here instead: the rows go quiet and stop responding while the
 * work runs, and then they are gone. The place a thing happens is the place to
 * report it.
 *
 * Counted rather than a plain set, so two operations touching the same row
 * cannot have the first to finish clear the second's mark.
 */

import { derived, writable, type Readable } from 'svelte/store';

const counts = writable<Map<string, number>>(new Map());

export const busyRowIds: Readable<ReadonlySet<string>> = derived(
    counts,
    ($counts) => new Set($counts.keys()),
);

/**
 * Marks these rows busy and hands back the release. Callers must release in a
 * `finally`: a row left marked is a row the user can no longer touch.
 */
export function markRowsBusy(ids: readonly string[]): () => void {
    const marked = ids.filter(Boolean);
    if (marked.length === 0) return () => {};

    counts.update((current) => {
        const next = new Map(current);
        for (const id of marked) next.set(id, (next.get(id) ?? 0) + 1);
        return next;
    });

    let released = false;
    return () => {
        if (released) return;
        released = true;
        counts.update((current) => {
            const next = new Map(current);
            for (const id of marked) {
                const remaining = (next.get(id) ?? 0) - 1;
                if (remaining > 0) next.set(id, remaining);
                else next.delete(id);
            }
            return next;
        });
    };
}
