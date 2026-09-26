/**
 * The aggregate behind one multi-file upload.
 *
 * A folder import is aggregated in Go, where the uploader already knows how the
 * whole job is going (see backend/services/file/upload_events.go). A plain
 * multi-file upload has no such job on the backend -- it is N independent files
 * that only the frontend knows were picked together -- so the same aggregate is
 * kept here, from the same per-file events, and reported in the same shape.
 *
 * Its cost per event is constant and its retained state is bounded by upload
 * concurrency rather than by the size of the batch: a file is tracked only
 * between its start and its outcome, and everything a finished file contributes
 * is folded into three numbers.
 */

import { MAX_TRANSFER_ITEMS, type TransferItem } from '../ui/notifications/notif-store';

export type UploadOutcome = 'done' | 'failed' | 'canceled';

export interface TransferBatchSnapshot {
    /** 0..100, counting a part-uploaded file as the fraction of it that is sent. */
    progress: number;
    itemsDone: number;
    itemsTotal: number;
    /** Bytes sent, from the files that finished plus the parts of those in flight. */
    bytes: number;
    items: readonly TransferItem[];
    itemsActive: number;
}

interface ActiveUpload {
    name: string;
    size: number;
    fraction: number;
}

export class TransferBatch {
    /** In-flight files only, in start order, so the row's list keeps its order. */
    private readonly active = new Map<number, ActiveUpload>();
    private readonly total: number;
    private done = 0;
    private failed = 0;
    private canceled = 0;
    private doneBytes = 0;

    constructor(total: number) {
        this.total = Math.max(0, Math.floor(total));
    }

    get settled(): number {
        return this.done + this.failed + this.canceled;
    }

    get succeeded(): number {
        return this.done;
    }

    get stopped(): number {
        return this.canceled;
    }

    get failures(): number {
        return this.failed;
    }

    start(id: number, name: string, size: number): void {
        this.active.set(id, { name, size: Math.max(0, size), fraction: 0 });
    }

    /** Per-file percent. Ignores a file that has already settled or was never started. */
    progress(id: number, percent: number): void {
        const file = this.active.get(id);
        if (!file) return;
        file.fraction = Math.max(file.fraction, Math.max(0, Math.min(1, percent / 100)));
    }

    settle(id: number, outcome: UploadOutcome): void {
        const file = this.active.get(id);
        this.active.delete(id);
        if (outcome === 'done') {
            this.done += 1;
            if (file) this.doneBytes += file.size;
            return;
        }
        if (outcome === 'canceled') this.canceled += 1;
        else this.failed += 1;
    }

    /**
     * Closes out every file that has not reported an outcome.
     *
     * By the time the upload call returns, each file in the batch has finished
     * on the backend one way or another; anything still open here is an event
     * that never arrived, and a row that waits for it would never finish.
     */
    settleRemaining(outcome: UploadOutcome): void {
        for (const id of [...this.active.keys()]) this.settle(id, outcome);
        const missing = Math.max(0, this.total - this.settled);
        if (outcome === 'done') this.done += missing;
        else if (outcome === 'canceled') this.canceled += missing;
        else this.failed += missing;
    }

    /**
     * What the row should say now. Walking the active map is walking at most
     * `maxUploadConcurrency` entries, so this stays constant-time however many
     * files the batch holds.
     */
    snapshot(): TransferBatchSnapshot {
        let fractions = 0;
        let partialBytes = 0;
        const items: TransferItem[] = [];
        for (const [id, file] of this.active) {
            fractions += file.fraction;
            partialBytes += file.fraction * file.size;
            if (items.length < MAX_TRANSFER_ITEMS) {
                items.push({ key: String(id), name: file.name, progress: file.fraction * 100, total: file.size });
            }
        }
        const progress = this.total > 0
            ? Math.max(0, Math.min(100, ((this.settled + fractions) / this.total) * 100))
            : 0;
        return {
            progress,
            itemsDone: this.done,
            itemsTotal: this.total,
            bytes: Math.round(this.doneBytes + partialBytes),
            items,
            itemsActive: this.active.size,
        };
    }
}
