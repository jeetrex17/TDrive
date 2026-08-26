export interface ImportProgress {
    total: number;
    done: number;
    failed: number;
    progress: number;
}

export interface ImportProgressSnapshot {
    total?: unknown;
    done?: unknown;
    failed?: unknown;
    progress?: unknown;
}

export function createImportProgress(): ImportProgress {
    return { total: 0, done: 0, failed: 0, progress: 0 };
}

function monotonicCount(value: unknown, previous: number): number {
    const parsed = Number(value);
    if (!Number.isFinite(parsed)) return previous;
    return Math.max(previous, Math.floor(Math.max(0, parsed)));
}

function monotonicPercent(value: unknown, previous: number): number {
    const parsed = Number(value);
    if (!Number.isFinite(parsed)) return previous;
    return Math.max(previous, Math.min(100, Math.max(0, parsed)));
}

// Import progress arrives as backend aggregates, so every update is O(1) and
// the retained state stays constant regardless of the number of files.
export function reduceImportProgress(
    previous: ImportProgress,
    snapshot: ImportProgressSnapshot,
): ImportProgress {
    const total = monotonicCount(snapshot.total, previous.total);
    const done = monotonicCount(snapshot.done, previous.done);
    const failed = monotonicCount(snapshot.failed, previous.failed);
    let progress = monotonicPercent(snapshot.progress, previous.progress);

    if (total > 0 && done + failed >= total) progress = 100;
    return { total, done, failed, progress };
}
