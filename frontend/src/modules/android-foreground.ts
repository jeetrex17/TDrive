/**
 * Asking Android to let the app keep working after the user leaves it, and
 * telling the system notification what the work is actually doing.
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
 * callers need no platform test of their own. iOS cannot run transfers in the
 * background at all, so its eventual notification is a different sentence
 * ("paused -- reopen TDrive to finish") rather than a port of this one; the
 * shapes below are deliberately about the work, not about Android, so that
 * sentence can be built from the same values when the time comes.
 *
 * This module is also the single choke point between the page and the host,
 * which makes it the right place -- and the only place that cannot be bypassed
 * -- for the update floor every caller owes the notification shade.
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
    /** The item moving right now. Shown only where there is room for it. */
    detail?: string;
    /** Files finished, and files in the batch. Omit both where there is no count. */
    filesDone?: number;
    filesTotal?: number;
}

/**
 * What the user is left looking at once the work is over.
 *
 * The ongoing notification disappears with the service, which is right for a
 * transfer the user watched finish and wrong for one that ran while they were
 * elsewhere: all they would ever see is the absence of something they never
 * saw. A summary is the one notification here that is allowed to be an event.
 */
export interface ForegroundSummary {
    /** 'complete' when every byte landed; 'stopped' when the work did not finish. */
    outcome: 'complete' | 'stopped';
    title: string;
    text: string;
}

const UNAVAILABLE = 'This build cannot keep transfers running in the background.';

/**
 * The floor between two posts of the ongoing notification.
 *
 * Progress arrives many times a second. Android drops notification posts past
 * roughly ten a second per package, and long before that the cost is the
 * user's: a line whose bytes and estimate rewrite themselves faster than they
 * can be read is noise, and every post wakes the system UI to re-render the
 * shade. A second is about as fast as any of these numbers changes meaningfully
 * -- four times slower than the in-app progress emitter, twenty times slower
 * than the file picker's floor, both of which are drawing for someone who is
 * looking. Callers upstream may be slower still (the transfer queue coalesces
 * at 1.5s); this only clamps the worst case, and clamps it for every caller.
 */
const MIN_UPDATE_INTERVAL_MS = 1000;

let transferNotice: ForegroundNotice | null = null;
let photoBackupNotice: ForegroundNotice | null = null;
let hostUpdate: Promise<void> = Promise.resolve();

/** What the host was last told to show, and when, so updates can be coalesced. */
let shown: ForegroundNotice | null = null;
let shownAt = 0;
let pending: ReturnType<typeof setTimeout> | undefined;

/** Manual transfers outrank a backup: they are the thing the user just asked for. */
function desired(): ForegroundNotice | null {
    return transferNotice ?? photoBackupNotice;
}

/** Whether TDrive is the thing on screen, and so already saying all of this. */
function appIsVisible(): boolean {
    return typeof document !== 'undefined' && document.visibilityState === 'visible';
}

/**
 * Hands the host the current state.
 *
 * The payload is one object with `running` at its head and everything else
 * optional, so a field this build does not send behaves exactly as it did
 * before it existed and a host that predates a field ignores it rather than
 * failing: JSONObject.opt* returns the default for a key that is not there.
 * That is what lets the page and the host ship on different days.
 */
function post(summary?: ForegroundSummary): Promise<void> {
    const notice = desired();
    shown = notice;
    shownAt = Date.now();
    // A summary about work that is still going would be a lie, and one the user
    // is watching in the app is a second copy of the row already in front of
    // them. Both leave the plain teardown, which takes the notification away.
    const ending = summary && !notice && !appIsVisible() ? summary : undefined;
    const payload = notice ? { running: true, ...notice } : { running: false, ...ending };
    hostUpdate = hostUpdate.catch(() => undefined).then(async () => {
        await callBridge('foregroundService', [JSON.stringify(payload)], UNAVAILABLE);
    });
    return hostUpdate;
}

/**
 * Posts now, or schedules the post the floor is holding back.
 *
 * Starting, stopping and finishing are never delayed: a foreground service
 * cannot legally be started once the app is in the background, so a late start
 * is a start that fails, and a late stop is a notification outliving its work.
 * Only the updates in between are coalesced, and by trailing edge, so the last
 * thing the user is shown is the last thing that happened.
 */
function syncHost(summary?: ForegroundSummary): Promise<void> {
    clearTimeout(pending);
    pending = undefined;
    const notice = desired();
    const since = Date.now() - shownAt;
    if (!notice || !shown || since >= MIN_UPDATE_INTERVAL_MS) return post(summary);
    pending = setTimeout(() => {
        pending = undefined;
        void post().catch(() => undefined);
    }, MIN_UPDATE_INTERVAL_MS - since);
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

/**
 * Lets the process be killable again, and takes the notification down with it.
 * A summary replaces it with a dismissible line saying how the work ended --
 * only where the work really has ended, and only where the user was not there
 * to see it.
 */
export async function stopRunningInBackground(summary?: ForegroundSummary): Promise<void> {
    transferNotice = null;
    await syncHost(summary);
}

/** Adds/removes photo backup's ownership without stopping manual transfers. */
export async function setPhotoBackupBackgroundDemand(
    notice: ForegroundNotice | null,
    summary?: ForegroundSummary,
): Promise<void> {
    photoBackupNotice = notice;
    await syncHost(summary);
}
