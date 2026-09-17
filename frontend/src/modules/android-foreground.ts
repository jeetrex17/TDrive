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
    await callBridge('foregroundService', [JSON.stringify({ running: true, ...notice })], UNAVAILABLE);
}

/** Lets the process be killable again, and takes the notification down with it. */
export async function stopRunningInBackground(): Promise<void> {
    await callBridge('foregroundService', [JSON.stringify({ running: false })], UNAVAILABLE);
}
