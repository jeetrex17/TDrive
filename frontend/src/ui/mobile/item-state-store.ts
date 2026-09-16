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
import { historyEvents, type TransferEvent } from '../notifications/notif-store';
import { resolveItemState, type ItemState } from './item-state';

/** Pulls the file id back out of a "xfer:<direction>:<id>" transfer key. */
function transferFileId(transfer: TransferEvent): string {
    const parts = transfer.id.split(':');
    return parts.length >= 3 ? parts.slice(2).join(':') : '';
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
 * file id -> the transfer touching it. Where a file has more than one (a
 * download queued behind an upload), the newest wins: historyEvents is newest
 * first, so the first match is the one the user last caused.
 */
export const transfersByFile: Readable<Map<string, TransferEvent>> = derived(historyEvents, (events) => {
    const byFile = new Map<string, TransferEvent>();
    for (const event of events) {
        if (event.kind !== 'transfer') continue;
        const fileId = transferFileId(event);
        if (!fileId || byFile.has(fileId)) continue;
        byFile.set(fileId, event);
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
    const transfer = transfers.get(fileId);
    return resolveItemState({
        transfer: transfer ? transferInput(transfer) : undefined,
        offline: stored.offline,
        conflicted: stored.conflicted,
    });
}
