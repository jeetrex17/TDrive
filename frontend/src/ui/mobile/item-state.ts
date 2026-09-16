/**
 * Every offline/sync state a file can be in, and nothing else.
 *
 * This list is closed on purpose. The failure mode it exists to prevent is one
 * ambiguous cloud glyph standing for six different situations, where "is this
 * on my phone?", "is it still uploading?" and "did it break?" all look the
 * same and the only way to find out is to tap and wait. Each state below
 * answers one question the reader actually has, and every surface that shows
 * file status -- rows, the photo grid, the detail sheet, search results, the
 * transfer queue -- renders from this table so they cannot drift apart.
 *
 * Colour carries meaning here, so it is rationed to three roles:
 *   muted      nothing is happening and nothing is wrong
 *   accent     bytes are moving right now, and only then
 *   danger     a person has to decide something
 * `available-offline` is a confirmed state but deliberately not accent: if
 * accent marked both "working" and "finished", a glance could no longer tell
 * them apart, which is the whole job of the badge.
 */

export type ItemState =
    | 'online-only'
    | 'queued'
    | 'downloading'
    | 'available-offline'
    | 'syncing'
    | 'conflict'
    | 'failed';

export type ItemStateTone = 'muted' | 'accent' | 'danger';

export interface ItemStateDescriptor {
    /** Lucide glyph name, resolved to a component by ItemStatus.svelte. */
    readonly glyph: string;
    /** Spoken and written label. Short enough to sit in a row, plain enough to be believed. */
    readonly label: string;
    readonly tone: ItemStateTone;
    /** Whether the glyph turns; only the two states where bytes are moving do. */
    readonly spins: boolean;
    /**
     * Whether the row grows a second line to explain itself. Only the two
     * states a person must act on earn the extra height -- everything else
     * stays on the standard row so a list of ordinary files reads evenly.
     */
    readonly needsExplanation: boolean;
    /** Sentence shown on the explaining line and in the detail sheet. */
    readonly detail: string;
}

export const ITEM_STATES: Readonly<Record<ItemState, ItemStateDescriptor>> = {
    'online-only': {
        glyph: 'cloud',
        label: 'Online only',
        tone: 'muted',
        spins: false,
        needsExplanation: false,
        detail: 'Stored in your vault. Opening it downloads it.',
    },
    queued: {
        glyph: 'clock',
        label: 'Queued',
        tone: 'muted',
        spins: false,
        needsExplanation: false,
        detail: 'Waiting its turn in the transfer queue.',
    },
    downloading: {
        glyph: 'cloud-download',
        label: 'Downloading',
        tone: 'accent',
        spins: true,
        needsExplanation: false,
        detail: 'Coming down to this device now.',
    },
    'available-offline': {
        glyph: 'circle-check',
        label: 'Available offline',
        tone: 'muted',
        spins: false,
        needsExplanation: false,
        detail: 'On this device. Opens without a connection.',
    },
    syncing: {
        glyph: 'refresh-cw',
        label: 'Syncing',
        tone: 'accent',
        spins: true,
        needsExplanation: false,
        detail: 'Sending your change up to the vault.',
    },
    conflict: {
        glyph: 'triangle-alert',
        label: 'Conflict',
        tone: 'danger',
        spins: false,
        needsExplanation: true,
        detail: 'Changed in two places. Choose which version to keep.',
    },
    failed: {
        glyph: 'circle-alert',
        label: 'Failed',
        tone: 'danger',
        spins: false,
        needsExplanation: true,
        detail: 'The last transfer did not finish.',
    },
};

/** Every state, in the order a file usually travels through them. */
export const ITEM_STATE_ORDER: readonly ItemState[] = [
    'online-only',
    'queued',
    'downloading',
    'available-offline',
    'syncing',
    'conflict',
    'failed',
];

export function itemStateDescriptor(state: ItemState): ItemStateDescriptor {
    return ITEM_STATES[state];
}

/**
 * True when the badge should take a tap and open the transfer queue. Only the
 * states the queue can say more about are worth the trip: a tap that opens an
 * empty or unrelated screen is worse than a badge that does nothing.
 */
export function opensQueue(state: ItemState): boolean {
    return state === 'queued' || state === 'downloading' || state === 'syncing' || state === 'failed';
}

/**
 * The state a file is in, given what the projection knows and what the
 * transfer queue is doing with it. Transfers win over stored state: a file
 * that is on the device *and* being re-uploaded should read "syncing", because
 * that is the part that is still in motion and could still go wrong.
 */
export interface ItemStateInput {
    /** A transfer touching this file right now, if any. */
    readonly transfer?: { status: 'queued' | 'active' | 'paused' | 'failed' | 'done'; direction: 'up' | 'down' } | undefined;
    /** The projection says the local copy diverged from the vault. */
    readonly conflicted?: boolean;
    /** A complete local copy exists and is pinned to stay. */
    readonly offline?: boolean;
}

export function resolveItemState(input: ItemStateInput): ItemState {
    // A conflict outranks everything: no amount of transfer progress makes the
    // question of which version to keep go away, and quietly syncing over it
    // would be the one outcome the user cannot undo.
    if (input.conflicted) return 'conflict';

    const transfer = input.transfer;
    if (transfer && transfer.status !== 'done') {
        if (transfer.status === 'failed') return 'failed';
        // Paused reads as queued rather than as its own badge: from the row's
        // point of view both mean "not moving, will move later", and the
        // transfer queue is where the reason belongs.
        if (transfer.status === 'queued' || transfer.status === 'paused') return 'queued';
        return transfer.direction === 'up' ? 'syncing' : 'downloading';
    }

    return input.offline ? 'available-offline' : 'online-only';
}
