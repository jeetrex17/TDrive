/**
 * Keeping transfers alive once the user leaves the app, and saying so.
 *
 * A phone reclaims a backgrounded process whenever it feels like it, so an
 * upload survived only as long as the user stared at it. This watches the same
 * queue the Transfers tab draws from and asks the host to hold the process open
 * for exactly as long as there is something in it (modules/android-foreground).
 *
 * Two rules shape everything below, and both are about not being a nuisance:
 *
 * The service starts the instant work appears, with no debounce. It is
 * tempting to wait a second or two so a quick upload never costs a service at
 * all -- but Android 12+ refuses to start a foreground service once the app is
 * in the background, and a backgrounded WebView's timers are frozen anyway, so
 * a delayed start is a start that fails in precisely the case this exists for.
 * Keeping short transfers quiet is the notification's job instead: the platform
 * holds a new foreground-service notification back for ten seconds, and one
 * that finishes inside that is never seen.
 *
 * Stopping, by contrast, waits. A queue that hands off from one file to the
 * next can read as empty for a tick, and tearing the service down and standing
 * it back up around every gap is the churn worth avoiding.
 */

import {
    canRunInBackground,
    runInBackground,
    stopRunningInBackground,
    type ForegroundNotice,
} from '../../modules/android-foreground';
import { activeTransfers, type TransferEvent } from '../notifications/notif-store';
import {
    formatEta,
    formatSizePair,
    transferDetail,
    transferPercent,
    transferredBytes,
} from '../notifications/transfer-view';

/**
 * The floor between two notifications. Progress arrives many times a second,
 * and a line that rewrites itself at that rate is unreadable as well as
 * wasteful -- the numbers on it do not mean anything new that often.
 */
const UPDATE_INTERVAL_MS = 1500;

/** How long an empty queue has to stay empty before the process is let go. */
const STOP_GRACE_MS = 2500;

/** What the host has been told, and when, so nothing is repeated needlessly. */
export interface BackgroundState {
    running: boolean;
    sent: ForegroundNotice | null;
    sentAt: number;
    /** When the queue drained, or 0 while there is still work. */
    idleSince: number;
}

export const IDLE_BACKGROUND_STATE: BackgroundState = {
    running: false,
    sent: null,
    sentAt: 0,
    idleSince: 0,
};

/** What to do about the host this tick, and when to come back if nothing yet. */
export interface BackgroundPlan {
    /** Start the service, or replace what its notification says. */
    notice: ForegroundNotice | null;
    stop: boolean;
    /** Re-decide at this time; 0 when nothing is waiting on the clock. */
    wakeAt: number;
}

const NOTHING_TO_DO: BackgroundPlan = { notice: null, stop: false, wakeAt: 0 };

/**
 * The ongoing notification's two lines, or null when the queue is empty.
 *
 * One transfer can speak for itself, and does: the row on the Transfers tab
 * already works out the best sentence available about a single transfer, and
 * this is the same sentence. Several have to be added up, because a count in
 * the title over one file's numbers underneath would be a notification that
 * contradicts itself.
 */
export function describeTransfers(
    transfers: readonly TransferEvent[],
    now = Date.now(),
): ForegroundNotice | null {
    if (transfers.length === 0) return null;
    if (transfers.length === 1) {
        const only = transfers[0];
        return {
            title: `${verb(transfers)} ${only.name || 'file'}`,
            text: transferDetail(only, now),
            progress: transferPercent(only) ?? -1,
        };
    }

    let files = 0;
    let done = 0;
    let total = 0;
    let speed = 0;
    for (const transfer of transfers) {
        // A folder is one transfer carrying many files, and the count the user
        // recognises is the files, not the folders they happen to be in.
        files += Math.max(1, transfer.itemsTotal || 0);
        speed += Math.max(0, transfer.speed);
        // Both sides of the pair, or neither: counting bytes moved for a
        // transfer whose size is still unknown is how a notification comes to
        // claim "1.2 GB of 800 MB".
        if (transfer.total > 0) {
            total += transfer.total;
            done += transferredBytes(transfer);
        }
    }

    const parts: string[] = [];
    const remaining = total - done;
    if (total > 0) parts.push(formatSizePair(Math.min(done, total), total));
    // The queue drains one transfer at a time, so what is left over the rate it
    // is leaving at is the estimate that matches what the user will experience.
    if (remaining > 0 && speed > 0) parts.push(formatEta(remaining / speed));

    return {
        title: `${verb(transfers)} ${files} files`,
        text: parts.join(' · ') || 'Preparing…',
        progress: total > 0 ? Math.round((done / total) * 100) : -1,
    };
}

/** "Transferring" only when the queue really is going both ways at once. */
function verb(transfers: readonly TransferEvent[]): string {
    const uploads = transfers.filter((transfer) => transfer.direction === 'up').length;
    if (uploads === transfers.length) return 'Uploading';
    if (uploads === 0) return 'Downloading';
    return 'Transferring';
}

/**
 * Decides what the host should be told, given what it was told last. Pure, and
 * the only place any of these rules live: the shell below is a subscription and
 * a timer.
 */
export function planForegroundService(
    state: BackgroundState,
    transfers: readonly TransferEvent[],
    now: number,
): { state: BackgroundState; plan: BackgroundPlan } {
    const notice = describeTransfers(transfers, now);

    if (!notice) {
        if (!state.running) return { state: IDLE_BACKGROUND_STATE, plan: NOTHING_TO_DO };
        const since = state.idleSince || now;
        if (now - since < STOP_GRACE_MS) {
            return {
                state: { ...state, idleSince: since },
                plan: { notice: null, stop: false, wakeAt: since + STOP_GRACE_MS },
            };
        }
        return { state: IDLE_BACKGROUND_STATE, plan: { notice: null, stop: true, wakeAt: 0 } };
    }

    // Work arriving inside the grace period cancels the stop that was waiting.
    const busy: BackgroundState = { ...state, idleSince: 0 };
    if (!state.running || !state.sent) {
        return {
            state: { running: true, sent: notice, sentAt: now, idleSince: 0 },
            plan: { notice, stop: false, wakeAt: 0 },
        };
    }
    if (same(state.sent, notice)) return { state: busy, plan: NOTHING_TO_DO };
    // A different title means the work itself changed -- a new file, the last
    // upload giving way to a download -- which is worth being prompt about in a
    // way that another percent is not.
    if (state.sent.title !== notice.title || now - state.sentAt >= UPDATE_INTERVAL_MS) {
        return {
            state: { ...busy, sent: notice, sentAt: now },
            plan: { notice, stop: false, wakeAt: 0 },
        };
    }
    // Held back rather than dropped: without the wake, a transfer that stops
    // reporting leaves the notification stuck on the last line that got through.
    return { state: busy, plan: { notice: null, stop: false, wakeAt: state.sentAt + UPDATE_INTERVAL_MS } };
}

function same(a: ForegroundNotice, b: ForegroundNotice): boolean {
    return a.title === b.title && a.text === b.text && a.progress === b.progress;
}

/**
 * Watches the queue for as long as the phone shell is mounted. Inert wherever
 * the host has no such service, which is iOS and every desktop build.
 */
export function activateBackgroundTransfers(): () => void {
    if (!canRunInBackground()) return () => {};

    let state = IDLE_BACKGROUND_STATE;
    let queue: readonly TransferEvent[] = [];
    let timer: ReturnType<typeof setTimeout> | undefined;

    const evaluate = (): void => {
        clearTimeout(timer);
        timer = undefined;
        const now = Date.now();
        const result = planForegroundService(state, queue, now);
        state = result.state;
        if (result.plan.notice) void runInBackground(result.plan.notice).catch(report);
        if (result.plan.stop) void stopRunningInBackground().catch(report);
        if (result.plan.wakeAt) timer = setTimeout(evaluate, Math.max(0, result.plan.wakeAt - now));
    };

    const unsubscribe = activeTransfers.subscribe((transfers) => {
        queue = transfers;
        evaluate();
    });

    return () => {
        unsubscribe();
        clearTimeout(timer);
        // The shell going away takes the transfers with it, so whatever is
        // still held open is a notification about work that no longer exists.
        if (state.running) void stopRunningInBackground().catch(report);
        state = IDLE_BACKGROUND_STATE;
    };
}

function report(cause: unknown): void {
    console.warn('background transfers:', cause);
}
