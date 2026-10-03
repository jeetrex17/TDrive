import { writable } from 'svelte/store';
import { isMobilePlatform, onRuntimeEvent, selectFiles, type RuntimeUnsubscribe } from '../api';
import {
    cancelResumableUpload,
    listResumableUploads,
    normalizeResumableUpload,
    pauseResumableUpload,
    resumeResumableUpload,
    type ResumableUpload,
} from '../api/resumable-uploads';
import { sidebarState } from '../ui/sidebar/sidebar-store';
import { humanizeBackendError } from './errors';
import { notify } from './notifications';
import { linkResumableUpload } from './notif-bell';
import { appActions } from './app-actions';
import { withTransferFlow } from './transfers';

/** Active jobs are never reconstructed from localStorage. This is the backend's view. */
export const resumableUploads = writable<ResumableUpload[]>([]);

let activeChannelId: number | null = null;
let generation = 0;
const busy = new Set<string>();
const starting = new Set<string>();

function forCurrentChannel(jobs: readonly ResumableUpload[]): ResumableUpload[] {
    return jobs.filter((job) => job.channelId === activeChannelId && job.status !== 'completed');
}

/** A list response from an old drive must never replace a newer drive's jobs. */
export async function refreshResumableUploads(): Promise<void> {
    const request = ++generation;
    if (activeChannelId === null) {
        resumableUploads.set([]);
        return;
    }
    try {
        const jobs = await listResumableUploads();
        if (request === generation) resumableUploads.set(forCurrentChannel(jobs));
    } catch (error) {
        // Keep the last confirmed view. A transient list error must not turn
        // durable checkpoints into an empty transfer panel.
        console.error('Could not load resumable uploads:', error);
    }
}

/** Called once by the dashboard, not once per bell open. */
export function activateResumableUploads(): () => void {
    if (isMobilePlatform()) return () => {};
    const unsubscribeDrive = sidebarState.subscribe(({ activeChannelId: nextId }) => {
        if (nextId === activeChannelId) return;
        activeChannelId = nextId;
        resumableUploads.set([]);
        void refreshResumableUploads();
    });
    const unsubscribeEvent: RuntimeUnsubscribe = onRuntimeEvent('resumable_upload_changed', (payload) => {
        const job = normalizeResumableUpload(payload);
        if (!job) return;
        const raw = payload as Record<string, unknown>;
        const uploadId = Number(raw.upload_id);
        if (raw.upload_id !== undefined && Number.isSafeInteger(uploadId) && uploadId >= 0) {
            linkResumableUpload(uploadId, job.jobId);
        }
        if (job.channelId !== activeChannelId) return;
        generation += 1;
        resumableUploads.update((current) => forCurrentChannel([
            ...current.filter((entry) => entry.jobId !== job.jobId),
            job,
        ]));
        if (job.status === 'completed') appActions().refreshFiles();
    });
    return () => {
        generation += 1;
        activeChannelId = null;
        unsubscribeDrive();
        unsubscribeEvent();
        resumableUploads.set([]);
        busy.clear();
        starting.clear();
    };
}

async function runAction(jobId: string, action: () => Promise<void>): Promise<void> {
    if (busy.has(jobId)) return;
    busy.add(jobId);
    try {
        await action();
    } catch (error) {
        notify({ level: 'error', title: 'Upload could not continue', body: humanizeBackendError(error) });
    } finally {
        busy.delete(jobId);
        await refreshResumableUploads();
    }
}

/** The Wails promise lasts for the whole upload; controls must stay usable meanwhile. */
function startResume(jobId: string, chooseSource: boolean, checkingStatus = false): Promise<void> {
    if (starting.has(jobId)) return Promise.resolve();
    starting.add(jobId);
    return withTransferFlow(async () => {
        let sourcePath = '';
        if (chooseSource) {
            const paths = await selectFiles();
            if (paths.length === 0) return;
            if (paths.length !== 1) {
                notify({ level: 'warning', title: 'Choose one file', body: 'Select the original file for this upload.' });
                return;
            }
            sourcePath = paths[0];
        }
        const result = await resumeResumableUpload(jobId, sourcePath);
        if (!result.result.ok && result.result.error.code !== 'canceled' && !checkingStatus) {
            notify({ level: 'error', title: 'Upload could not continue', body: humanizeBackendError(result.result.error) });
        }
        if (result.result.ok) appActions().refreshFiles();
    })
        .catch((error: unknown) => {
            if (!checkingStatus) notify({ level: 'error', title: 'Upload could not continue', body: humanizeBackendError(error) });
        })
        .finally(() => {
            starting.delete(jobId);
            void refreshResumableUploads();
        });
}

export function resumeUpload(jobId: string): void { void startResume(jobId, false); }

export function chooseSourceAndResume(jobId: string): void { void startResume(jobId, true); }

/** The backend reconciles history before retrying with the same manifest random ID. */
export function checkUploadStatus(jobId: string): Promise<void> { return startResume(jobId, false, true); }

export function pauseUpload(jobId: string): Promise<void> {
    return runAction(jobId, async () => {
        const result = await pauseResumableUpload(jobId);
        if (!result.ok) throw result.error;
    });
}

export function discardUpload(jobId: string): Promise<void> {
    return runAction(jobId, async () => {
        const result = await cancelResumableUpload(jobId);
        if (!result.ok) throw result.error;
    });
}
