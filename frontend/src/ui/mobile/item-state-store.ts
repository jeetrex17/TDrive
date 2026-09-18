/**
 * What state each file is in right now, keyed by the id a row already carries.
 *
 * This is the one join between the transfer queue and everything that draws a
 * file: rows, the photo grid, search results, the detail sheet. Keeping it in a
 * single derived store is what stops the surfaces disagreeing -- the failure it
 * prevents is a row that still says "downloading" while the queue has already
 * moved the same file to "failed", because each read the truth separately.
 *
 * Stored state (a pinned offline copy, a conflict) is not in the projection
 * yet. When it arrives it joins here, and every surface picks it up without
 * changing: they all ask this store, and this store owns the precedence.
 */

import { derived, type Readable } from 'svelte/store';
import { state } from '../../state';
import { historyEvents, type TransferEvent } from '../notifications/notif-store';
import { itemStateDescriptor, resolveItemState, type ItemState } from './item-state';

/**
 * Pulls the row id back out of an "xfer:<direction>:<kind>:<id>" transfer key.
 *
 * Only a key that names its kind is a claim about a row. The download queue
 * keys its jobs "file:<driveId>:<msgId>" / "folder:<driveId>:<d:id>". The
 * drive segment prevents a row in another drive with the same Telegram message
 * id inheriting a stale transfer badge after a switch.
 *
 * A key without one -- "xfer:up:3", "xfer:up:import" -- is not a file id at
 * all: an upload is numbered by its position in the batch, so "3" is the
 * fourth file of this upload and also, on a young drive, the message id of
 * some unrelated row. Mapping those marked a stranger's row "Failed". They are
 * dropped instead, and nothing is lost: a file being uploaded has no row to
 * badge until it commits, by which time the transfer is over. When an upload
 * one day knows the id it is writing to, keying it "file:<id>" is all it takes
 * to appear here.
 */
function transferFileKey(transfer: TransferEvent): string {
    const parts = transfer.id.split(':');
    if (parts.length < 4) return '';
    if (parts[2] !== 'file' && parts[2] !== 'folder') return '';
    const sourceChannelId = Number(parts[3]);
    if (parts.length >= 5 && Number.isSafeInteger(sourceChannelId) && sourceChannelId > 0) {
        return `${sourceChannelId}:${parts.slice(4).join(':')}`;
    }
    // History created before drive-scoped transfer ids is intentionally still
    // understood. It cannot be disambiguated, but it expires from the bounded
    // history and keeps a completed upgrade from hiding an old failure.
    return parts.slice(3).join(':');
}

/**
 * Maps a transfer's status onto the smaller vocabulary resolveItemState takes.
 * A cancelled transfer is deliberately absent: cancelling returns the file to
 * whatever it was before, so the row should show its stored state, not a
 * lingering badge for something the user already called off.
 */
function transferInput(transfer: TransferEvent) {
    switch (transfer.status) {
        case 'queued':
        case 'paused':
        case 'active':
        case 'failed':
        case 'done':
            return { status: transfer.status, direction: transfer.direction } as const;
        default:
            return undefined;
    }
}

/**
 * drive id + file id -> the transfer touching it. Legacy rows use the bare
 * file id. Where a file has more than one (a
 * download queued behind an upload), the newest wins: historyEvents is newest
 * first, so the first match is the one the user last caused.
 */
export const transfersByFile: Readable<Map<string, TransferEvent>> = derived(historyEvents, (events) => {
    const byFile = new Map<string, TransferEvent>();
    for (const event of events) {
        if (event.kind !== 'transfer') continue;
        const fileKey = transferFileKey(event);
        if (!fileKey || byFile.has(fileKey)) continue;
        byFile.set(fileKey, event);
    }
    return byFile;
});

/**
 * The state to draw for one file. Pass what the projection knows about it; the
 * live transfer half is filled in here.
 */
export function itemStateFor(
    transfers: Map<string, TransferEvent>,
    fileId: string,
    stored: { offline?: boolean; conflicted?: boolean } = {},
): ItemState {
    const activeChannelId = Number(state.activeChannel?.id);
    const scopedKey = Number.isSafeInteger(activeChannelId) && activeChannelId > 0
        ? `${activeChannelId}:${fileId}`
        : '';
    const transfer = (scopedKey ? transfers.get(scopedKey) : undefined) ?? transfers.get(fileId);
    return resolveItemState({
        transfer: transfer ? transferInput(transfer) : undefined,
        offline: stored.offline,
        conflicted: stored.conflicted,
    });
}

/**
 * The ids of the files whose rows have to grow a second line explaining
 * themselves, for the drive that is on screen.
 *
 * Derived from the transfers, never from the rows. The two are not the same
 * size: history is capped at HISTORY_CAP entries, while a folder is however
 * many files the drive holds, and this answer is wanted again on every
 * progress event. Walking the rows meant a full pass over a ten-thousand-row
 * folder several times a second to name at most a handful of files.
 *
 * `scope` is any value whose identity changes when the active drive does --
 * the published file-list view is the one the caller has. Transfer keys are
 * drive-scoped and resolved here against the live active channel, so a set
 * built for the previous drive cannot be reused after a switch: message ids
 * repeat across drives, and a stale id would make a stranger's row tall.
 *
 * Unchanged membership publishes the *previous* set, not an equal one. The
 * same identity-as-cache-key reasoning row-lookup.ts relies on: callers hold
 * this in a derived, and a fresh collection per tick would re-run everything
 * downstream -- the virtualiser's metrics, and with them the rendered window.
 */
const DRIVE_SCOPED_KEY = /^\d+:/;

let explainedFrom: Map<string, TransferEvent> | null = null;
let explainedScope: unknown = null;
let explainedIds: ReadonlySet<string> = new Set<string>();

export function explainedFileIds(
    transfers: Map<string, TransferEvent>,
    scope: unknown = null,
): ReadonlySet<string> {
    if (transfers === explainedFrom && scope === explainedScope) return explainedIds;

    const activeChannelId = Number(state.activeChannel?.id);
    const prefix = Number.isSafeInteger(activeChannelId) && activeChannelId > 0
        ? `${activeChannelId}:`
        : '';
    const next = new Set<string>();
    for (const key of transfers.keys()) {
        const id = rowIdForTransferKey(key, prefix);
        if (!id || next.has(id)) continue;
        // Asked rather than answered here: a file with both a scoped and a
        // legacy entry has a precedence rule, and it lives in one place.
        if (itemStateDescriptor(itemStateFor(transfers, id)).needsExplanation) next.add(id);
    }

    explainedFrom = transfers;
    explainedScope = scope;
    if (!sameIds(next, explainedIds)) explainedIds = next;
    return explainedIds;
}

/** The row id a transfer key names in the active drive, or '' for none. */
function rowIdForTransferKey(key: string, prefix: string): string {
    if (prefix && key.startsWith(prefix)) return key.slice(prefix.length);
    // A leading integer segment is what makes a key drive-scoped, so one that
    // carries another drive's names no row on screen. Everything else is a
    // legacy bare id, which is the row id as it stands.
    return DRIVE_SCOPED_KEY.test(key) ? '' : key;
}

function sameIds(a: ReadonlySet<string>, b: ReadonlySet<string>): boolean {
    if (a.size !== b.size) return false;
    for (const id of a) if (!b.has(id)) return false;
    return true;
}
