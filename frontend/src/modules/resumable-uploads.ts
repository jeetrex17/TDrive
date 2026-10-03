import { get } from 'svelte/store';
import { onRuntimeEvent, selectFiles, type RuntimeUnsubscribe } from '../api';
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
import { tryWithTransferFlow, withTransferFlow } from './transfers';
import { resumableUploads } from './resumable-upload-store';

export { resumableUploads } from './resumable-upload-store';

let activeChannelId: number | null = null;
let generation = 0;
const busy = new Set<string>();
const starting = new Set<string>();
let active = false;
let retryTimer: ReturnType<typeof setTimeout> | null = null;
let retryCount = 0;
let retrying = false;
let nativeConnected: boolean | null = null;
let lastRetriedJobId: string | null = null;

function stopRetryTimer(): void {
    if (retryTimer !== null) clearTimeout(retryTimer);
    retryTimer = null;
}

function canRetryInBackground(): boolean {
    return active && activeChannelId !== null
        && document.visibilityState !== 'hidden'
        && (nativeConnected ?? navigator.onLine !== false);
}

function nativeNetworkChanged(payload: unknown): void {
    let value = payload;
    if (typeof value === 'string') {
        try { value = JSON.parse(value); } catch { return; }
    }
    if (!value || typeof value !== 'object' || Array.isArray(value)) return;
    const connected = (value as Record<string, unknown>).connected;
    if (typeof connected !== 'boolean') return;
    nativeConnected = connected;
    if (connected) scheduleNetworkRetry(true);
    else stopRetryTimer();
}

/** Only a transport failure is safe to retry without the user's decision. */
function scheduleNetworkRetry(wake = false): void {
    if (wake) {
        retryCount = 0;
        stopRetryTimer();
    }
    if (!canRetryInBackground()) {
        stopRetryTimer();
        return;
    }
    const waiting = getWaitingUpload();
    if (!waiting) {
        stopRetryTimer();
        retryCount = 0;
        return;
    }
    if (retrying || retryTimer !== null) return;
    const delay = retryCount === 0 ? 0 : Math.min(5_000 * 2 ** (retryCount - 1), 60_000);
    retryTimer = setTimeout(() => {
        retryTimer = null;
        void retryWaitingUpload();
    }, delay);
}

function getWaitingUpload(): ResumableUpload | undefined {
    const waiting = get(resumableUploads).filter((job) => (
        job.channelId === activeChannelId && job.status === 'waiting_network' && !starting.has(job.jobId)
    ));
    if (waiting.length === 0) return undefined;
    const lastIndex = waiting.findIndex((job) => job.jobId === lastRetriedJobId);
    return waiting[(lastIndex + 1) % waiting.length];
}

async function retryWaitingUpload(): Promise<void> {
    const job = canRetryInBackground() ? getWaitingUpload() : undefined;
    if (!job || retrying) return;
    const channelId = activeChannelId;
    retrying = true;
    starting.add(job.jobId);
    lastRetriedJobId = job.jobId;
    try {
        await tryWithTransferFlow(async () => {
            if (!canRetryInBackground() || activeChannelId !== channelId) return;
            const result = await resumeResumableUpload(job.jobId);
            if (result.result.ok && activeChannelId === channelId) appActions().refreshFiles();
        });
    } catch (error) {
        // The journal keeps the actionable state; a background retry should
        // leave that state visible instead of producing repeated error toasts.
        console.error('Could not retry resumable upload:', error);
    } finally {
        starting.delete(job.jobId);
        retryCount += 1;
        await refreshResumableUploads();
        retrying = false;
        scheduleNetworkRetry();
    }
}

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
        if (request === generation) {
            resumableUploads.set(forCurrentChannel(jobs));
            scheduleNetworkRetry();
        }
    } catch (error) {
        // Keep the last confirmed view. A transient list error must not turn
        // durable checkpoints into an empty transfer panel.
        console.error('Could not load resumable uploads:', error);
    }
}

/** Called once by the dashboard, not once per bell open. */
export function activateResumableUploads(): () => void {
    active = true;
    const unsubscribeDrive = sidebarState.subscribe(({ activeChannelId: nextId }) => {
        if (nextId === activeChannelId) return;
        activeChannelId = nextId;
        retryCount = 0;
        lastRetriedJobId = null;
        stopRetryTimer();
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
        scheduleNetworkRetry();
        if (job.status === 'completed') appActions().refreshFiles();
    });
    const unsubscribeAndroidNetwork = onRuntimeEvent('android:NetworkChanged', nativeNetworkChanged);
    const unsubscribeIOSNetwork = onRuntimeEvent('ios:NetworkChanged', nativeNetworkChanged);
    const handleOnline = () => {
        nativeConnected = null;
        scheduleNetworkRetry(true);
    };
    const handleOffline = () => {
        nativeConnected = null;
        stopRetryTimer();
    };
    const handleVisibility = () => {
        if (document.visibilityState === 'hidden') stopRetryTimer();
        else scheduleNetworkRetry(true);
    };
    window.addEventListener('online', handleOnline);
    window.addEventListener('offline', handleOffline);
    document.addEventListener('visibilitychange', handleVisibility);
    return () => {
        active = false;
        stopRetryTimer();
        retryCount = 0;
        retrying = false;
        lastRetriedJobId = null;
        generation += 1;
        activeChannelId = null;
        unsubscribeDrive();
        unsubscribeEvent();
        unsubscribeAndroidNetwork();
        unsubscribeIOSNetwork();
        nativeConnected = null;
        window.removeEventListener('online', handleOnline);
        window.removeEventListener('offline', handleOffline);
        document.removeEventListener('visibilitychange', handleVisibility);
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
