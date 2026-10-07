// Pure decisions behind the file list's keyboard model. They are kept out of
// the DOM so type-ahead matching, paging and range math can be checked
// directly, and so the delegated listener stays a thin layer over them.

/**
 * The row a type-ahead buffer should jump to, searching forward from
 * `currentIndex` and wrapping, or -1 when nothing matches. Matching is
 * case-insensitive and anchored at the start of the name.
 *
 * One character cycles to the next match so holding a key walks the matches;
 * a longer buffer refines in place and may stay on the current row, so typing
 * more of a name never skips past it.
 */
export function typeAheadIndex(names: readonly string[], query: string, currentIndex: number): number {
    const needle = query.toLowerCase();
    const total = names.length;
    if (!needle || total === 0) return -1;
    const start = needle.length === 1 ? currentIndex + 1 : currentIndex;
    for (let step = 0; step < total; step += 1) {
        const index = (((start + step) % total) + total) % total;
        if (names[index]?.toLowerCase().startsWith(needle)) return index;
    }
    return -1;
}

/** The index a PageUp/PageDown lands on: a viewport of rows away, clamped. */
export function pageJumpIndex(currentIndex: number, total: number, pageRows: number, direction: 1 | -1): number {
    if (total <= 0) return -1;
    const page = Math.max(1, pageRows);
    return Math.min(total - 1, Math.max(0, currentIndex + direction * page));
}

/** The selection keys spanning anchor..target inclusive, in row order. */
export function rangeSelectionKeys(
    rows: readonly { selectionKey: string }[],
    anchorIndex: number,
    targetIndex: number,
): string[] {
    const total = rows.length;
    if (total === 0) return [];
    const clamp = (value: number) => Math.min(total - 1, Math.max(0, value));
    const lo = clamp(Math.min(anchorIndex, targetIndex));
    const hi = clamp(Math.max(anchorIndex, targetIndex));
    const keys: string[] = [];
    for (let index = lo; index <= hi; index += 1) keys.push(rows[index].selectionKey);
    return keys;
}
