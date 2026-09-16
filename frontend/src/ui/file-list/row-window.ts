/**
 * Which slice of a long list to render, and how tall the spacers above and
 * below it have to be.
 *
 * The list is windowed by height, not by count: the rows that are not rendered
 * are replaced by two empty divs, so every offset here is load-bearing. Get one
 * wrong and the scrollbar lies -- the content under the thumb jumps as the
 * window slides.
 *
 * Nearly every row is the same height, but not all of them: a file whose last
 * transfer failed grows a line explaining why. One height for all of them put
 * the window out by that difference for every explaining row above it, which on
 * a phone reads as the list lurching while you scroll. Their indices are passed
 * in and counted instead.
 */

export interface RowMetrics {
    /** Height of an ordinary row, including its margins. */
    readonly rowHeight: number;
    /** Height of a row that draws the extra explaining line. */
    readonly tallRowHeight: number;
    /** Indices of those rows, ascending. Usually empty, never long. */
    readonly tallIndices: readonly number[];
}

export interface RowWindow {
    /** Spacer height above the rendered rows. */
    readonly before: number;
    /** First rendered index. */
    readonly start: number;
    /** One past the last rendered index. */
    readonly end: number;
    /** Spacer height below the rendered rows. */
    readonly after: number;
}

/** Where row `index` starts, measuring from the top of the list. */
export function rowOffset(index: number, metrics: RowMetrics): number {
    const height = Math.max(1, metrics.rowHeight);
    const extra = Math.max(0, metrics.tallRowHeight - height);
    let taller = 0;
    for (const tall of metrics.tallIndices) {
        if (tall >= index) break;
        taller += 1;
    }
    return index * height + taller * extra;
}

/** The last row that has started by `y`. */
function indexAt(y: number, total: number, metrics: RowMetrics): number {
    let low = 0;
    let high = total;
    while (low < high) {
        const mid = (low + high + 1) >> 1;
        if (rowOffset(mid, metrics) <= y) low = mid;
        else high = mid - 1;
    }
    return low;
}

/**
 * The rows to render for a viewport, plus `overscan` rows of slack on each
 * side so a flick has something to land on before the next scroll event.
 */
export function rowWindowFor(
    total: number,
    scrollTop: number,
    viewportHeight: number,
    overscan: number,
    metrics: RowMetrics,
): RowWindow {
    const start = Math.max(0, indexAt(scrollTop, total, metrics) - overscan);
    const end = Math.min(total, indexAt(scrollTop + viewportHeight, total, metrics) + 1 + overscan);
    return {
        before: rowOffset(start, metrics),
        start,
        end,
        after: rowOffset(total, metrics) - rowOffset(end, metrics),
    };
}
