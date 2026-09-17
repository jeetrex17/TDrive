import type { MediaBucket } from '../../api/gallery';

interface BucketLayout extends MediaBucket { top: number; height: number; rows: number; firstRow: number }
export interface GalleryLayout {
    buckets: BucketLayout[];
    columns: number;
    cellSize: number;
    rowPitch: number;
    headerHeight: number;
    height: number;
    rowCount: number;
}
export interface GalleryRow { key: string; top: number; indices: number[]; rowIndex: number }
export interface GalleryHeader { key: string; label: string; top: number }

/** Only month summaries are measured. No row/item array is built for offscreen photos. */
export function createGalleryLayout(buckets: readonly MediaBucket[], width: number, mobile: boolean): GalleryLayout {
    const gap = mobile ? 2 : 5;
    const columns = Math.max(1, Math.floor((Math.max(1, width) + gap) / ((mobile ? 112 : 172) + gap)));
    const cellSize = Math.max(1, (width - gap * (columns - 1)) / columns);
    const rowPitch = cellSize + gap;
    const headerHeight = mobile ? 44 : 48;
    let height = 0;
    let rowCount = 0;
    const measured = buckets.map((bucket) => {
        const rows = Math.ceil(bucket.count / columns);
        const entry = { ...bucket, top: height, height: headerHeight + rows * rowPitch - gap + (mobile ? 8 : 18), rows, firstRow: rowCount };
        height += entry.height;
        rowCount += rows;
        return entry;
    });
    return { buckets: measured, columns, cellSize, rowPitch, headerHeight, height, rowCount };
}

/** Binary search keeps random scrollbar jumps O(log months), then O(visible rows). */
function floorIndex<T>(items: readonly T[], target: number, key: (item: T) => number): number {
    let low = 0;
    let high = items.length;
    while (low < high) {
        const middle = (low + high) >>> 1;
        if (key(items[middle]) <= target) low = middle + 1;
        else high = middle;
    }
    return Math.max(0, low - 1);
}

export function offsetForIndex(layout: GalleryLayout, index: number): number {
    const bucket = layout.buckets[floorIndex(layout.buckets, index, (entry) => entry.startIndex)];
    if (!bucket) return 0;
    return bucket.top + layout.headerHeight + Math.floor(Math.max(0, index - bucket.startIndex) / layout.columns) * layout.rowPitch;
}

export function indexAtOffset(layout: GalleryLayout, offset: number): number {
    const bucket = layout.buckets[floorIndex(layout.buckets, offset, (entry) => entry.top)];
    if (!bucket) return 0;
    const row = Math.max(0, Math.min(bucket.rows - 1, Math.floor((offset - bucket.top - layout.headerHeight) / layout.rowPitch)));
    return bucket.startIndex + row * layout.columns;
}

function monthLabel(key: string): string {
    const match = /^(\d{4})-(\d{2})$/.exec(key);
    if (!match) return 'Unknown date';
    // The server buckets in UTC. Parsing the key avoids shifting a boundary
    // photo into the previous month on a device west of UTC.
    return new Date(Date.UTC(Number(match[1]), Number(match[2]) - 1, 1)).toLocaleDateString(undefined, { month: 'long', year: 'numeric', timeZone: 'UTC' });
}

export function galleryWindow(layout: GalleryLayout, scrollTop: number, viewportHeight: number): { rows: GalleryRow[]; headers: GalleryHeader[] } {
    const buffer = layout.rowPitch * 2;
    const start = Math.max(0, scrollTop - buffer);
    const end = scrollTop + Math.max(1, viewportHeight) + buffer;
    const rows: GalleryRow[] = [];
    const headers: GalleryHeader[] = [];
    const first = floorIndex(layout.buckets, start, (entry) => entry.top);
    for (let index = first; index < layout.buckets.length; index += 1) {
        const bucket = layout.buckets[index];
        if (bucket.top > end) break;
        headers.push({ key: bucket.key, label: monthLabel(bucket.key), top: Math.min(Math.max(bucket.top, scrollTop), bucket.top + bucket.height - layout.headerHeight) });
        const firstRow = Math.max(0, Math.floor((start - bucket.top - layout.headerHeight) / layout.rowPitch));
        const lastRow = Math.min(bucket.rows, Math.ceil((end - bucket.top - layout.headerHeight) / layout.rowPitch));
        for (let row = firstRow; row < lastRow; row += 1) {
            const startIndex = bucket.startIndex + row * layout.columns;
            const count = Math.min(layout.columns, bucket.startIndex + bucket.count - startIndex);
            rows.push({ key: `${bucket.key}:${startIndex}`, top: bucket.top + layout.headerHeight + row * layout.rowPitch, indices: Array.from({ length: count }, (_, offset) => startIndex + offset), rowIndex: bucket.firstRow + row + 1 });
        }
    }
    return { rows, headers };
}
