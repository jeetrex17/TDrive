/**
 * What a transfer row says, worked out once and away from any markup.
 *
 * The phone row and its tests both read from here, so what a state is called is
 * decided in one place. The rule behind every answer below is that a row never
 * reports a number it does not have: a queue position is not 0%, and a folder
 * still being walked has no total to be a fraction of. Claiming either is how a
 * transfer that is waiting its turn comes to look like one that has stalled.
 */

import { formatBytes } from '../../utils';
import type { TransferEvent } from './notif-store';

/**
 * The states a row actually draws differently.
 *
 * `queued` and `paused` look alike -- stopped, with nothing wrong -- but they
 * are not the same sentence. A queued transfer is behind other work and will
 * start on its own; a paused one stopped because iOS suspended the app, and
 * saying it is "waiting its turn" would send the reader off looking for the
 * queue ahead of it that does not exist.
 */
export type TransferPhase =
    | 'waiting'
    | 'paused'
    | 'preparing'
    | 'running'
    | 'canceling'
    | 'done'
    | 'failed'
    | 'canceled';

/** Longer than this and an estimate is a guess, so the row keeps it to itself. */
const MAX_SENSIBLE_ETA_SECONDS = 24 * 60 * 60;

/** Below this the estimate swings wildly between ticks; "moments" is truer. */
const ETA_FLOOR_SECONDS = 5;

export function transferPhase(transfer: TransferEvent): TransferPhase {
    switch (transfer.status) {
        case 'done': return 'done';
        case 'failed': return 'failed';
        case 'canceled': return 'canceled';
        case 'canceling': return 'canceling';
        case 'queued': return 'waiting';
        case 'paused': return 'paused';
        default:
            // Started, but with nothing measurable yet: a folder being walked
            // before its first byte moves, or a file whose size is still coming.
            return transfer.total <= 0 && (transfer.itemsTotal || 0) <= 0 && (transfer.progress || 0) <= 0
                ? 'preparing'
                : 'running';
    }
}

/** True while the transfer still has somewhere to go, so the row keeps a bar. */
export function isMoving(phase: TransferPhase): boolean {
    return phase === 'waiting' || phase === 'paused' || phase === 'preparing' || phase === 'running';
}

/**
 * How full the bar is, or null when the honest answer is "no idea" and the bar
 * should say so by moving instead.
 */
export function transferPercent(transfer: TransferEvent): number | null {
    const phase = transferPhase(transfer);
    if (phase === 'preparing') return null;
    if (phase === 'done') return 100;
    if (!isMoving(phase)) return null;
    return Math.max(0, Math.min(100, transfer.progress || 0));
}

/**
 * Bytes moved so far. Progress is the more reliable of the two figures -- every
 * backend reports it, and byte counts arrive late on some paths -- so it wins
 * where the two disagree, and the pair can never read "0 B of 40 MB, 60%".
 */
export function transferredBytes(transfer: TransferEvent): number {
    if (transfer.total <= 0) return Math.max(0, transfer.bytes || 0);
    const fromProgress = ((transfer.progress || 0) / 100) * transfer.total;
    return Math.min(transfer.total, Math.max(transfer.bytes || 0, fromProgress));
}

/**
 * Seconds left at the current rate, or null when there is nothing to divide.
 *
 * This is the figure a transfer screen exists to answer and the one the row
 * never had: bytes and speed are the workings, "2 min left" is the answer.
 */
export function etaSeconds(transfer: TransferEvent): number | null {
    if (transferPhase(transfer) !== 'running') return null;
    if (transfer.total <= 0 || transfer.speed <= 0) return null;
    const remaining = transfer.total - transferredBytes(transfer);
    if (remaining <= 0) return null;
    const seconds = remaining / transfer.speed;
    if (!Number.isFinite(seconds) || seconds > MAX_SENSIBLE_ETA_SECONDS) return null;
    return seconds;
}

/** "2 min left". Compact, because it shares a line with two other figures. */
export function formatEta(seconds: number): string {
    if (seconds < ETA_FLOOR_SECONDS) return 'Almost done';
    if (seconds < 60) return `${Math.ceil(seconds)}s left`;
    const minutes = Math.ceil(seconds / 60);
    if (minutes < 60) return `${minutes} min left`;
    const hours = Math.floor(minutes / 60);
    const rest = minutes % 60;
    return rest === 0 ? `${hours} hr left` : `${hours} hr ${rest} min left`;
}

/**
 * "679 MB of 1.1 GB", or "679 of 942 MB" when both land on the same unit.
 * Naming a unit twice when it is the same unit both times reads as two
 * quantities where the person is looking at one, and costs a phone row width
 * it does not have.
 */
export function formatSizePair(done: number, total: number): string {
    const left = formatBytes(done);
    const right = formatBytes(total);
    const leftUnit = left.slice(left.lastIndexOf(' ') + 1);
    const rightUnit = right.slice(right.lastIndexOf(' ') + 1);
    if (leftUnit && leftUnit === rightUnit) return `${left.slice(0, left.lastIndexOf(' '))} of ${right}`;
    return `${left} of ${right}`;
}

/** "2 min ago". Coarse on purpose: a finished transfer is history, not a clock. */
export function formatAge(timestamp: number, now = Date.now()): string {
    if (!timestamp) return '';
    const seconds = Math.max(0, Math.floor((now - timestamp) / 1000));
    if (seconds < 60) return 'just now';
    const minutes = Math.floor(seconds / 60);
    if (minutes < 60) return `${minutes} min ago`;
    const hours = Math.floor(minutes / 60);
    if (hours < 24) return `${hours} hr ago`;
    const days = Math.floor(hours / 24);
    if (days < 7) return `${days}d ago`;
    return new Date(timestamp).toLocaleDateString();
}

/**
 * The one line under the name: everything true about this transfer right now,
 * in the order it is worth reading, with the parts that do not apply left out
 * rather than spelled as zero.
 *
 * Order matters on a narrow screen. How far along comes first because it is
 * what the bar is already saying and the eye checks it against; the estimate
 * next, because it is the reason the screen was opened; the rate last, because
 * it changes every tick and says least about where the work has got to. The
 * row clips from the right, so the least useful figure is the first to go.
 */
export function transferDetail(transfer: TransferEvent, now = Date.now()): string {
    const phase = transferPhase(transfer);
    switch (phase) {
        case 'waiting': return 'Waiting its turn';
        case 'paused': return 'Paused';
        case 'preparing': return 'Preparing…';
        case 'canceling': return 'Stopping…';
        case 'canceled': return joinDetail(['Canceled', formatAge(transfer.finishedAt, now)]);
        case 'failed': return joinDetail(['Failed', formatAge(transfer.finishedAt, now)]);
        case 'done': return joinDetail(['Done', formatAge(transfer.finishedAt, now)]);
        default: break;
    }

    const parts: string[] = [];
    const items = transfer.itemsTotal || 0;
    if (items > 0) parts.push(`${transfer.itemsDone || 0} of ${items} files`);
    if (transfer.total > 0) parts.push(formatSizePair(transferredBytes(transfer), transfer.total));
    else if (items === 0) parts.push(`${Math.round(transfer.progress || 0)}%`);

    const eta = etaSeconds(transfer);
    if (eta !== null) parts.push(formatEta(eta));
    if (transfer.speed > 0) parts.push(`${formatBytes(transfer.speed)}/s`);
    return joinDetail(parts);
}

function joinDetail(parts: readonly string[]): string {
    return parts.filter(Boolean).join(' · ');
}

/** Spoken description of the whole row, for a screen reader reaching the bar. */
export function transferAriaLabel(transfer: TransferEvent, now = Date.now()): string {
    const verb = transfer.direction === 'up' ? 'Uploading' : 'Downloading';
    return `${verb} ${transfer.name || 'transfer'}. ${transferDetail(transfer, now)}`;
}
