// Pure view logic for the trash. Everything here is a function of the rows
// plus the current time, so the panel stays a renderer and the wording is
// testable without a DOM.
import type { TrashEntry } from '../../api/trash';
import { formatBytes } from '../../utils';

const MINUTE = 60_000;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;

/** Under this much time left, the countdown earns a warning tone. */
const URGENT_WINDOW = 2 * DAY;

export interface PurgeCountdown {
    label: string;
    /** True once the item is about to go, which colours the line. */
    urgent: boolean;
}

/**
 * Newest deletion first, so the item a user just lost is the one they land on.
 * The name breaks ties, which keeps a bulk delete -- one timestamp across many
 * rows -- in a stable, readable order instead of the backend's row order.
 */
export function sortedByDeletion(entries: readonly TrashEntry[]): TrashEntry[] {
    return [...entries].sort((left, right) => (
        right.deletedAt - left.deletedAt || left.name.localeCompare(right.name)
    ));
}

/**
 * How long is left before the backend purges the item, in the words a person
 * would use. A missing or already-elapsed deadline reads as imminent rather
 * than as a negative number: the item is gone at the next sweep either way.
 */
export function purgeCountdown(purgeAfter: number, now: number): PurgeCountdown {
    const remaining = purgeAfter - now;
    if (!purgeAfter || remaining <= 0) return { label: 'Purging soon', urgent: true };
    if (remaining < HOUR) return { label: 'Less than an hour left', urgent: true };
    if (remaining < DAY) {
        const hours = Math.round(remaining / HOUR);
        return { label: hours === 1 ? '1 hour left' : `${hours} hours left`, urgent: true };
    }
    // Between one and two days out, "tomorrow" is only true if the purge
    // really does land on tomorrow's date: 30 hours from late evening is the
    // day after, and saying otherwise would promise the wrong deadline.
    if (remaining < 2 * DAY) {
        return { label: fallsTomorrow(purgeAfter, now) ? 'Purges tomorrow' : '1 day left', urgent: true };
    }
    const days = Math.floor(remaining / DAY);
    return { label: `${days} days left`, urgent: remaining < URGENT_WINDOW };
}

/** Whether the deadline lands on the calendar day after the current one. */
function fallsTomorrow(purgeAfter: number, now: number): boolean {
    const today = new Date(now);
    const deadline = new Date(purgeAfter);
    today.setHours(0, 0, 0, 0);
    deadline.setHours(0, 0, 0, 0);
    return deadline.getTime() - today.getTime() === DAY;
}

/** Where the item came from. An empty parent path is the drive's own root. */
export function originLabel(parentPath: string): string {
    return parentPath.trim() || 'Drive root';
}

/**
 * The truncatable half of a row's second line: where the item was, and how big
 * it was. The countdown is deliberately not part of it -- it is rendered
 * beside this and never allowed to be the text that gets cut.
 */
export function entryOrigin(entry: TrashEntry): string {
    const origin = originLabel(entry.parentPath);
    return entry.kind === 'file' && entry.size > 0 ? `${origin} · ${formatBytes(entry.size)}` : origin;
}

/** The dialog's subtitle: what is in here, and what it is holding on to. */
export function trashSummary(entries: readonly TrashEntry[]): string {
    if (entries.length === 0) return '';
    const bytes = entries.reduce((total, entry) => total + entry.size, 0);
    const items = entries.length === 1 ? '1 item' : `${entries.length} items`;
    return bytes > 0 ? `${items} · ${formatBytes(bytes)}` : items;
}
