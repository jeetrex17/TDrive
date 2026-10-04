import { get, writable } from 'svelte/store';
import { isAndroidPlatform, isIOSPlatform, onRuntimeEvent } from '../api';
import {
    discardResumableDownload,
    listResumableDownloads,
    normalizeResumableDownload,
    pauseResumableDownload,
    resumeDownload,
    type ResumableDownload,
} from '../api/resumable-downloads';
import { sidebarState } from '../ui/sidebar/sidebar-store';
import { rememberDownloadSharePath } from '../ui/mobile/mobile-shell-store';
import { state } from '../state';
import { canSaveToDownloads, saveToDownloads } from './android-downloads';
import { downloadRunBusy, onDownloadRunIdle, tryBeginDownloadRun } from './download-run';
import { humanizeBackendError } from './errors';
import { openEncryptionPasswordModal } from './modals/encryption-password';
import { notify } from './notifications';
import { markTransferDone, pushTransferStart, removeTransfer, setTransferNote, updateTransferProgress } from './notif-bell';

/** The backend journal owns these bytes. This store is only its current UI view. */
export const resumableDownloads = writable<ResumableDownload[]>([]);

let activeChannelId: number | null = null;
let generation = 0;
let active = false;
let requestSequence = 0;
const starting = new Set<string>();
const busy = new Set<string>();
const exporting = new Set<string>();
const samples = new Map<string, { bytes: number; at: number; speed: number }>();

function transferId(job: ResumableDownload): string {
    return `file:${job.channelId}:${job.logicalMsgId}`;
}

function isQueuedInThisProcess(job: ResumableDownload): boolean {
    return state.activeDownloadId === transferId(job);
}

function recordCompleted(job: ResumableDownload, location?: string): void {
    const id = transferId(job);
    pushTransferStart({ id, direction: 'down', name: job.name, total: job.totalBytes });
    updateTransferProgress({ id, direction: 'down', progress: 100, bytes: job.totalBytes, total: job.totalBytes });
    if (location) setTransferNote({ id, direction: 'down', note: `Saved to ${location}` });
    if (isIOSPlatform() && job.savedPath) rememberDownloadSharePath(`xfer:down:${id}`, job.savedPath);
    markTransferDone({ id, direction: 'down' });
    resumableDownloads.update((jobs) => jobs.filter((entry) => entry.jobId !== job.jobId));
}

/** Android's stable job receipt makes this safe after a lost bridge callback. */
async function finishRecoveredDownload(job: ResumableDownload): Promise<void> {
    if (activeChannelId !== job.channelId || isQueuedInThisProcess(job) || exporting.has(job.jobId)) return;
    exporting.add(job.jobId);
    try {
        let location = '';
        if (isAndroidPlatform()) {
            if (!canSaveToDownloads()) return;
            if (!job.savedPath) return;
            location = await saveToDownloads(job.savedPath, job.jobId);
            if (!location) throw new Error('Downloads did not confirm a save location');
        }
        if (activeChannelId !== job.channelId) return;
        const result = await discardResumableDownload(job.channelId, job.jobId);
        if (!result.ok) throw result.error;
        if (activeChannelId === job.channelId) recordCompleted(job, location || undefined);
    } catch (error) {
        // Keep the completed job for another attempt. Android's native receipt
        // prevents a second public copy if the first callback was lost.
        console.error('Could not finish download export:', error);
    } finally {
        exporting.delete(job.jobId);
    }
}

/** Called after a new download's visible save or Android export has succeeded. */
export async function acknowledgeCompletedDownload(channelId: number, jobId: string): Promise<void> {
    if (!jobId) return;
    try {
        const result = await discardResumableDownload(channelId, jobId);
        if (!result.ok) throw result.error;
        resumableDownloads.update((jobs) => jobs.filter((job) => job.jobId !== jobId));
    } catch (error) {
        // The verified output is already saved. A future list can reconcile
        // this leftover completed journal without fetching Telegram bytes.
        console.error('Could not clear completed download journal:', error);
    }
}

function putJob(job: ResumableDownload): void {
    if (job.channelId !== activeChannelId) return;
    resumableDownloads.update((current) => {
        const previous = current.find((entry) => entry.jobId === job.jobId);
        const next = { ...job, speed: job.status === 'downloading' ? previous?.speed ?? 0 : 0 };
        return [...current.filter((entry) => entry.jobId !== job.jobId), next];
    });
    if (job.status === 'completed') void finishRecoveredDownload(job);
}

/** A late list result from another drive cannot replace the current drive. */
export async function refreshResumableDownloads(): Promise<void> {
    const channelId = activeChannelId;
    const request = ++generation;
    if (channelId === null) {
        resumableDownloads.set([]);
        return;
    }
    try {
        const jobs = await listResumableDownloads(channelId);
        if (request !== generation || channelId !== activeChannelId) return;
        const old = get(resumableDownloads);
        resumableDownloads.set(jobs.filter((job) => job.channelId === channelId).map((job) => ({
            ...job,
            speed: job.status === 'downloading' ? old.find((entry) => entry.jobId === job.jobId)?.speed ?? 0 : 0,
        })));
        for (const job of jobs) if (job.channelId === channelId && job.status === 'completed') void finishRecoveredDownload(job);
    } catch (error) {
        // A list failure must not erase the last confirmed checkpoint view.
        console.error('Could not load resumable downloads:', error);
    }
}

export function activateResumableDownloads(): () => void {
    active = true;
    const unsubscribeDrive = sidebarState.subscribe(({ activeChannelId: next }) => {
        if (next === activeChannelId) return;
        activeChannelId = next;
        resumableDownloads.set([]);
        samples.clear();
        void refreshResumableDownloads();
    });
    const unsubscribeChanged = onRuntimeEvent('resumable_download_changed', (payload) => {
        const job = normalizeResumableDownload(payload);
        if (!job || job.channelId !== activeChannelId) return;
        generation += 1;
        putJob(job);
    });
    const unsubscribeProgress = onRuntimeEvent('download_resume_progress', (rawId, rawBytes, rawTotal) => {
        const jobId = String(rawId ?? '');
        const verifiedBytes = Number(rawBytes);
        const totalBytes = Number(rawTotal);
        if (!jobId || !Number.isFinite(verifiedBytes) || verifiedBytes < 0
            || !Number.isFinite(totalBytes) || totalBytes < 0) return;
        const now = performance.now();
        const previous = samples.get(jobId);
        const elapsed = previous ? (now - previous.at) / 1000 : 0;
        const rate = elapsed > 0 && elapsed < 15 && verifiedBytes >= (previous?.bytes ?? 0)
            ? (verifiedBytes - (previous?.bytes ?? 0)) / elapsed
            : 0;
        const speed = rate > 0 ? (previous?.speed ? previous.speed * 0.7 + rate * 0.3 : rate) : 0;
        samples.set(jobId, { bytes: verifiedBytes, at: now, speed });
        resumableDownloads.update((jobs) => jobs.map((job) => job.jobId === jobId && job.status === 'downloading'
            ? { ...job, verifiedBytes: Math.min(totalBytes, verifiedBytes), totalBytes, speed }
            : job));
    });
    let nativeConnected: boolean | null = null;
    let waking = false;
    const canWake = () => active && document.visibilityState !== 'hidden'
        && (nativeConnected ?? navigator.onLine) !== false;
    const wake = () => {
        if (waking || !canWake() || downloadRunBusy()) return;
        waking = true;
        void (async () => {
            const attempted = new Set<string>();
            try {
                while (canWake() && !downloadRunBusy()) {
                    const waiting = get(resumableDownloads).find((job) =>
                        job.status === 'waiting_network' && !attempted.has(job.jobId));
                    if (!waiting) break;
                    attempted.add(waiting.jobId);
                    await resumePausedDownload(waiting.jobId, true);
                }
                if (attempted.size === 0) await refreshResumableDownloads();
            } finally {
                waking = false;
            }
        })();
    };
    const nativeNetworkChanged = (payload: unknown) => {
        let value = payload;
        if (typeof value === 'string') {
            try { value = JSON.parse(value); } catch { return; }
        }
        if (!value || typeof value !== 'object' || Array.isArray(value)) return;
        const connected = (value as Record<string, unknown>).connected;
        if (typeof connected !== 'boolean') return;
        nativeConnected = connected;
        if (connected) wake();
    };
    const unsubscribeAndroidNetwork = onRuntimeEvent('android:NetworkChanged', nativeNetworkChanged);
    const unsubscribeIOSNetwork = onRuntimeEvent('ios:NetworkChanged', nativeNetworkChanged);
    const browserOnline = () => { nativeConnected = null; wake(); };
    const browserOffline = () => { nativeConnected = null; };
    window.addEventListener('online', browserOnline);
    window.addEventListener('offline', browserOffline);
    document.addEventListener('visibilitychange', wake);
    const unsubscribeIdle = onDownloadRunIdle(wake);
    return () => {
        active = false;
        generation += 1;
        activeChannelId = null;
        unsubscribeDrive();
        unsubscribeChanged();
        unsubscribeProgress();
        unsubscribeAndroidNetwork();
        unsubscribeIOSNetwork();
        window.removeEventListener('online', browserOnline);
        window.removeEventListener('offline', browserOffline);
        document.removeEventListener('visibilitychange', wake);
        unsubscribeIdle();
        resumableDownloads.set([]);
        starting.clear();
        busy.clear();
        samples.clear();
    };
}

export async function resumePausedDownload(jobId: string, quiet = false): Promise<void> {
    const job = get(resumableDownloads).find((entry) => entry.jobId === jobId);
    if (!job || starting.has(jobId)) return;
    const releaseRun = tryBeginDownloadRun();
    if (!releaseRun) {
        if (!quiet) notify({ level: 'info', title: 'Another download is running', body: 'Pause it before resuming this download.' });
        return;
    }
    starting.add(jobId);
    try {
        const requestId = `${jobId}@${++requestSequence}`;
        let result = await resumeDownload(job.channelId, jobId, requestId);
        if (!result.result.ok && result.result.error.code === 'encryption_password_required') {
            if (await openEncryptionPasswordModal()) {
                result = await resumeDownload(job.channelId, jobId, requestId);
            } else return;
        }
        if (!result.result.ok && result.result.error.code !== 'canceled' && !quiet) {
            notify({ level: 'error', title: 'Download could not continue', body: humanizeBackendError(result.result.error) });
        }
        await refreshResumableDownloads();
    } catch (error) {
        if (!quiet) notify({ level: 'error', title: 'Download could not continue', body: humanizeBackendError(error) });
    } finally {
        starting.delete(jobId);
        releaseRun();
    }
}

async function runAction(jobId: string, action: (job: ResumableDownload) => Promise<void>): Promise<void> {
    const job = get(resumableDownloads).find((entry) => entry.jobId === jobId);
    if (!job || busy.has(jobId)) return;
    busy.add(jobId);
    try {
        await action(job);
        await refreshResumableDownloads();
    } catch (error) {
        notify({ level: 'error', title: 'Download could not be changed', body: humanizeBackendError(error) });
    } finally {
        busy.delete(jobId);
    }
}

export function pauseDownload(jobId: string): Promise<void> {
    return runAction(jobId, async (job) => {
        const result = await pauseResumableDownload(job.channelId, jobId);
        if (!result.ok) throw result.error;
    });
}

export function discardDownload(jobId: string): Promise<void> {
    return runAction(jobId, async (job) => {
        const result = await discardResumableDownload(job.channelId, jobId);
        if (!result.ok) throw result.error;
        removeTransfer({ id: transferId(job), direction: 'down' });
        resumableDownloads.update((jobs) => jobs.filter((entry) => entry.jobId !== jobId));
    });
}

export function saveRecoveredDownload(jobId: string): Promise<void> {
    const job = get(resumableDownloads).find((entry) => entry.jobId === jobId);
    return job ? finishRecoveredDownload(job) : Promise.resolve();
}
