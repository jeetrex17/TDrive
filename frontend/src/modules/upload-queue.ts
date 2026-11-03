import { addUploadActivity, finishUploadActivity, startUploadActivity, type NewUploadActivity } from '../ui/notifications/upload-activity-store';

export interface QueuedUpload {
    activity: NewUploadActivity;
    run: () => Promise<void>;
    release?: (reason: 'removed' | 'completed' | 'disposed') => void;
}
const MAX_WAITING_UPLOADS = 10;
let sequence = 0;
export function nextUploadActivityId(): string { return `upload-${Date.now()}-${++sequence}`; }
let removeWaiting: ((id: string) => void) | null = null;
export function removeQueuedUpload(id: string): void { removeWaiting?.(id); }

/** FIFO source ownership. Waiting closures are deliberately never persisted. */
export function createUploadQueue(options: { canRun: (activity: NewUploadActivity) => boolean }) {
    let waiting: QueuedUpload[] = [];
    let running = false;
    let disposed = false;
    const remove = (id: string) => {
        const entry = waiting.find((item) => item.activity.id === id);
        if (!entry) return;
        waiting = waiting.filter((item) => item !== entry);
        entry.release?.('removed');
        finishUploadActivity(id, 'canceled');
        void wake();
    };
    const wake = async (): Promise<void> => {
        if (disposed || running) return;
        // Order holds per drive. A batch for a drive that is not open waits
        // without holding back the open drive's batches.
        const entry = waiting.find((item) => options.canRun(item.activity));
        if (!entry) return;
        waiting = waiting.filter((item) => item !== entry);
        running = true;
        startUploadActivity(entry.activity.id);
        try {
            await entry.run();
        } catch (error) {
            if (!disposed) finishUploadActivity(entry.activity.id, 'failed', [], error instanceof Error ? error.message : String(error));
        } finally {
            entry.release?.(disposed ? 'disposed' : 'completed');
            running = false;
            if (!disposed) await wake();
        }
    };
    return {
        add(entry: QueuedUpload): Promise<void> {
            if (disposed) throw new Error('This upload session has ended.');
            if (waiting.length >= MAX_WAITING_UPLOADS) throw new Error('The upload queue is full. Keep at most 10 waiting batches.');
            addUploadActivity(entry.activity);
            waiting = [...waiting, entry];
            return wake();
        },
        wake,
        remove,
        get pending(): boolean { return running || waiting.length > 0; },
        waitingFor(driveId: number | undefined): boolean { return waiting.some((item) => item.activity.driveId === driveId); },
        register(): void { removeWaiting = remove; },
        dispose(): void {
            disposed = true;
            for (const entry of waiting) entry.release?.('disposed');
            waiting = [];
            if (removeWaiting === remove) removeWaiting = null;
        },
    };
}
