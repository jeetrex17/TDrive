/**
 * The wait between the file picker closing and the app having usable files.
 *
 * On Android a picked document is not a path: the host has to copy it out of
 * its content provider before anything can read it, and Wails reports the
 * selection only once every copy has landed. The picker activity is gone by
 * then, so that time is spent on TDrive's own screen with nothing to show for
 * it -- which is what made choosing a few photos feel like the app had hung.
 *
 * The folder picker already solved this: it puts a transfer row on screen
 * before its tree walk (see importAndroidFolder). This does the same for files,
 * off the progress the host now reports, so the wait reads as the copy it is.
 *
 * Nothing here is Android-specific except where the events come from. Desktop
 * pickers hand back paths with no copy and emit nothing, so this stays silent;
 * iOS copies behind its own picker, where the system is already showing its own
 * progress, so it stays silent there too.
 */

import { onRuntimeEvent } from '../api';
import { markTransferDone, pushTransferStart, updateTransferProgress } from './notif-bell';

/** Keys the single row for a selection. A string, so it cannot collide with
 *  the numeric per-file upload ids. */
const SELECTION_TRANSFER_ID = 'file-selection';

/**
 * How long a copy has to run before it is worth showing.
 *
 * A couple of small files finish inside the picker's own dismissal, and a row
 * that appears and vanishes in the same breath is noise that reads as a
 * glitch. Past this, the wait is long enough to need explaining.
 */
const VISIBLE_AFTER_MS = 220;

export interface SelectionCopyProgress {
    done: number;
    total: number;
    finished: boolean;
}

/**
 * Reads a host progress payload, or null if it is not one.
 *
 * Kept pure and exported for its own test: this is the seam where a host's
 * JSON meets typed code, and it is the part most likely to drift if a payload
 * ever gains a field.
 */
export function selectionCopyProgress(payload: unknown): SelectionCopyProgress | null {
    if (payload === null || typeof payload !== 'object') return null;
    const record = payload as Record<string, unknown>;
    const total = Number(record.total);
    const done = Number(record.done);
    if (!Number.isFinite(total) || total <= 0) return null;
    if (!Number.isFinite(done) || done < 0) return null;
    return {
        total,
        // A host that miscounts must not drive the row past its end.
        done: Math.min(done, total),
        finished: record.phase === 'done' || done >= total,
    };
}

function label(progress: SelectionCopyProgress): string {
    return progress.total === 1
        ? 'Preparing 1 file…'
        : `Preparing ${progress.total} files…`;
}

/**
 * Mirrors host copy progress into the transfer row for as long as one is
 * running. Returns the teardown, and is safe to activate more than once
 * because the row is keyed and replaced rather than appended.
 */
export function activateFileSelectionProgress(): () => void {
    // Only ever one selection is in flight -- the picker holds the transfer
    // lock -- so a single timer and flag describe the whole state.
    let revealTimer: ReturnType<typeof setTimeout> | undefined;
    let visible = false;
    let latest: SelectionCopyProgress | null = null;

    function reveal(): void {
        revealTimer = undefined;
        if (visible || !latest || latest.finished) return;
        visible = true;
        pushTransferStart({
            id: SELECTION_TRANSFER_ID,
            direction: 'up',
            name: label(latest),
            // Left at zero deliberately: a row's `total` is its byte count, and
            // it drives the size and speed in the row meta. The file count goes
            // through itemsTotal below, which is what renders "3 of 7". The
            // aggregate import row does the same for the same reason.
            total: 0,
        });
        paint(latest);
    }

    function paint(progress: SelectionCopyProgress): void {
        updateTransferProgress({
            id: SELECTION_TRANSFER_ID,
            direction: 'up',
            progress: Math.round((progress.done / progress.total) * 100),
            itemsDone: progress.done,
            itemsTotal: progress.total,
        });
    }

    function settle(): void {
        clearTimeout(revealTimer);
        revealTimer = undefined;
        if (visible) markTransferDone({ id: SELECTION_TRANSFER_ID, direction: 'up' });
        visible = false;
        latest = null;
    }

    const stop = onRuntimeEvent('common:filepicker', (payload) => {
        const progress = selectionCopyProgress(payload);
        if (!progress) return;
        latest = progress;

        if (progress.finished) {
            // The row is only ever a stand-in for the upload that follows it.
            // Closing it here hands the screen over rather than leaving two
            // rows describing the same files.
            settle();
            return;
        }
        if (visible) {
            paint(progress);
            return;
        }
        // The first tick arrives before any copying, so this timer measures the
        // copy itself rather than the round trip that announced it.
        if (revealTimer === undefined) revealTimer = setTimeout(reveal, VISIBLE_AFTER_MS);
    });

    return () => {
        stop();
        settle();
    };
}
