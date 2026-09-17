/**
 * What the file list's second column is currently reporting.
 *
 * Normally it is the date a file was added, and the header carries the Date
 * sort control. A search spans every folder in the drive, so the date stops
 * being the useful fact and the same column reports where each result lives
 * instead -- search puts the path in `metaLabel`, which is the field that
 * column draws.
 *
 * This exists because the header used to be relabelled by writing to the
 * column's textContent from the search module. That column is not a text node:
 * it wraps the Date sort button and its indicator, so assigning textContent
 * deleted the control outright and left Svelte updating nodes that were no
 * longer in the document. The button never came back, because what got restored
 * afterwards was the text captured from the whole subtree.
 *
 * A one-word label is not worth reaching across a module boundary for. The
 * header reads this, the search module sets it, and neither needs to know how
 * the other is built.
 */

import { writable } from 'svelte/store';

export type FileListColumnMode = 'date' | 'location';

export const fileListColumnMode = writable<FileListColumnMode>('date');

/** Leaving search returns the column to the drive's own ordering. */
export function resetFileListColumnMode(): void {
    fileListColumnMode.set('date');
}
