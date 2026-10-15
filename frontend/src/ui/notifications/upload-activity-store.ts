import { get, writable } from 'svelte/store';

export type UploadActivityStatus = 'queued' | 'active' | 'done' | 'failed' | 'canceled';
export interface UploadActivityFile {
    index: number;
    name: string;
    /** Where the file sits in what was selected ("Album/2024/a.jpg"); display only, never a local path. */
    path?: string;
    status: UploadActivityStatus;
    size?: number;
    progress?: number;
    message?: string;
}
export interface UploadActivity {
    id: string;
    name: string;
    driveId: number;
    driveTitle: string;
    destination: string;
    encrypt: boolean;
    addedAt: number;
    startedAt: number;
    finishedAt: number;
    status: UploadActivityStatus;
    files: readonly UploadActivityFile[];
    fileCount: number;
    transferId?: string;
    error?: string;
    resumableJobIds?: readonly string[];
}
export type NewUploadActivity = Omit<UploadActivity, 'addedAt' | 'startedAt' | 'finishedAt' | 'status'>;
export const MAX_UPLOAD_ACTIVITY_FILES = 10_000;
const MAX_UPLOAD_ACTIVITIES = 100;
// Source paths and document capabilities belong to the queue, never this
// session display model or the persisted transfer history.
export const uploadActivities = writable<UploadActivity[]>([]);

export function addUploadActivity(activity: NewUploadActivity): void {
    if (activity.files.length > MAX_UPLOAD_ACTIVITY_FILES) throw new Error('Keep queued selections under 10,000 files.');
    uploadActivities.update((current) => {
        let retained = [...current];
        const overBudget = () => retained.reduce((sum, entry) => sum + entry.files.length, activity.files.length) > MAX_UPLOAD_ACTIVITY_FILES
            || retained.length >= MAX_UPLOAD_ACTIVITIES;
        while (overBudget()) {
            let index = retained.length - 1;
            while (index >= 0 && (retained[index].status === 'active' || retained[index].status === 'queued')) index -= 1;
            if (index < 0) throw new Error('The upload queue can retain 10,000 files. Wait for a batch to finish or remove queued work.');
            retained = retained.filter((_, position) => position !== index);
        }
        return [{ ...activity, files: activity.files.map((file) => ({ ...file })), addedAt: Date.now(), startedAt: 0, finishedAt: 0, status: 'queued' }, ...retained];
    });
}

const isFinished = (entry: UploadActivity) => entry.status !== 'active' && entry.status !== 'queued';

/**
 * Adds or updates files a batch only learns about while it runs, as a folder
 * import does. When the session budget is full, older finished batches give up
 * their detail first; a running batch is never trimmed.
 */
export function mergeUploadActivityFiles(id: string, updates: readonly UploadActivityFile[]): void {
    if (!updates.length) return;
    uploadActivities.update((current) => {
        const target = current.find((entry) => entry.id === id);
        if (!target) return current;
        const byIndex = new Map(target.files.map((file) => [file.index, file]));
        for (const update of updates) byIndex.set(update.index, { ...byIndex.get(update.index), ...update });
        const files = [...byIndex.values()];
        let kept = current.filter((entry) => entry !== target);
        const total = () => kept.reduce((sum, entry) => sum + entry.files.length, files.length);
        while (total() > MAX_UPLOAD_ACTIVITY_FILES) {
            const oldest = kept.map(isFinished).lastIndexOf(true);
            if (oldest < 0) break;
            kept = kept.filter((_, position) => position !== oldest);
        }
        return current.filter((entry) => entry === target || kept.includes(entry))
            .map((entry) => entry === target ? { ...entry, files } : entry);
    });
}

export function updateUploadActivity(id: string, update: Partial<UploadActivity>): void {
    uploadActivities.update((current) => current.map((entry) => entry.id === id ? { ...entry, ...update } : entry));
}
export function startUploadActivity(id: string): void {
    updateUploadActivity(id, { status: 'active', startedAt: Date.now(), error: undefined });
}
export function updateUploadActivityFile(id: string, index: number, update: Partial<UploadActivityFile>): void {
    uploadActivities.update((current) => current.map((entry) => entry.id === id ? {
        ...entry, files: entry.files.map((file) => file.index === index ? { ...file, ...update } : file),
    } : entry));
}
type FinalFailure = { index: number; message: string; code?: string };
function finalFileStatus(file: UploadActivityFile, status: UploadActivityStatus, failures: ReadonlyMap<number, FinalFailure>): UploadActivityStatus {
    const failure = failures.get(file.index);
    if (failure) return failure.code === 'canceled' ? 'canceled' : 'failed';
    if (file.status === 'done') return 'done';
    if (!failures.size && (file.status === 'failed' || file.status === 'canceled')) return file.status;
    if (status === 'canceled') return 'canceled';
    // A structured result enumerates every failed input. Other indexes were
    // accepted; a thrown invocation instead supplies unknown-outcome failures.
    return status === 'done' || failures.size > 0 ? 'done' : status;
}
export function finishUploadActivity(id: string, status: UploadActivityStatus, failures: readonly FinalFailure[] = [], error?: string): void {
    const failed = new Map(failures.map((failure) => [failure.index, failure]));
    uploadActivities.update((current) => current.map((entry) => entry.id === id ? {
        ...entry, status, error, finishedAt: Date.now(), files: entry.files.map((file) => ({
            ...file, status: finalFileStatus(file, status, failed),
            progress: status === 'done' ? 100 : file.progress,
            message: failed.get(file.index)?.message ?? file.message,
        })),
    } : entry));
}
export function uploadActivityFilesPage(id: string, offset = 0, limit = 50): readonly UploadActivityFile[] {
    return get(uploadActivities).find((entry) => entry.id === id)?.files.slice(Math.max(0, offset), Math.max(0, offset) + Math.min(100, Math.max(0, limit))) ?? [];
}
export function clearUploadActivities(): void { uploadActivities.set([]); }

export function clearUploadActivityHistory(): void {
    uploadActivities.update((current) => current.filter((entry) => entry.status === 'active' || entry.status === 'queued'));
}
