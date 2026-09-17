/**
 * Asking Android to let the app keep working after the user leaves it.
 *
 * Nothing holds the process open today, so backgrounding the app mid-upload
 * leaves the system free to kill it and take the transfer with it. Android's
 * one sanctioned answer is a foreground service with an ongoing notification,
 * which the host runs on the app's behalf (WailsForegroundService); this is the
 * page's end of that. What the notification says arrives with every call, so
 * the line the user is watching stays true while the work moves.
 *
 * Every method here is a no-op away from Android: the host is the only thing
 * that exposes this, so `canRunInBackground` is false on iOS and desktop and
 * callers need no platform test of their own.
 */

import { callBridge, hasBridgeMethod } from './android-bridge';

/** The one line the user sees for as long as the work runs. */
export interface ForegroundNotice {
    /** What is happening: "Uploading 3 files". */
    title: string;
    /** How it is going: "679 MB of 1.1 GB · 2 min left". */
    text: string;
    /** 0..100, or negative while there is no honest figure to show. */
    progress: number;
}

const UNAVAILABLE = 'This build cannot keep transfers running in the background.';
let transferNotice: ForegroundNotice | null = null;
let photoBackupNotice: ForegroundNotice | null = null;
let hostUpdate: Promise<void> = Promise.resolve();

function syncHost(): Promise<void> {
    hostUpdate = hostUpdate.catch(() => undefined).then(async () => {
        const notice = transferNotice ?? photoBackupNotice;
        await callBridge('foregroundService', [JSON.stringify(notice ? { running: true, ...notice } : { running: false })], UNAVAILABLE);
    });
    return hostUpdate;
}

/** Whether this build can hold the process open at all. */
export function canRunInBackground(): boolean {
    return hasBridgeMethod('foregroundService');
}

/**
 * Starts the service, or replaces what its notification says if it is already
 * running. The host makes no distinction between the two, which is what keeps
 * the page from having to track state the system already owns.
 */
export async function runInBackground(notice: ForegroundNotice): Promise<void> {
    transferNotice = notice;
    await syncHost();
}

/** Lets the process be killable again, and takes the notification down with it. */
export async function stopRunningInBackground(): Promise<void> {
    transferNotice = null;
    await syncHost();
}

/** Adds/removes photo backup's ownership without stopping manual transfers. */
export async function setPhotoBackupBackgroundDemand(notice: ForegroundNotice | null): Promise<void> {
    photoBackupNotice = notice;
    await syncHost();
}
