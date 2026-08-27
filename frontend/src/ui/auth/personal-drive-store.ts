import { writable } from 'svelte/store';

export type PersonalDrivePhase = 'loading' | 'ready' | 'recovering' | 'discovery-error';

export interface PersonalDriveCandidate {
    id: string;
    title: string;
    created_at: number;
    has_activity: boolean;
    recommended: boolean;
}

export type DriveScanPhase = 'counting' | 'applying' | 'waiting';

/** One history-scan update from the backend, as emitted over the Wails bridge. */
export interface DriveScanProgress {
    phase: DriveScanPhase;
    pages_done: number;
    pages_total: number;
    messages_done: number;
    messages_total: number;
    wait_seconds: number;
}

export interface PersonalDriveSetupState {
    phase: PersonalDrivePhase;
    candidates: PersonalDriveCandidate[];
    /** Short, user-facing headline for the current error, if any. */
    error: string;
    /** Underlying error text, shown muted under the headline. */
    detail: string;
    /** A channel was created remotely but local setup did not finish. */
    createRetry: boolean;
    /** Latest counting/applying update, or null before the first one lands. */
    scan: DriveScanProgress | null;
    /** Seconds Telegram asked us to pause for; 0 when not throttled. */
    waitSeconds: number;
}

function count(value: unknown): number {
    return typeof value === 'number' && Number.isFinite(value) && value > 0 ? Math.floor(value) : 0;
}

/**
 * Parses one progress payload off the Wails bridge, which is untyped. Returns
 * null for anything unrecognised so a future or malformed event is ignored
 * rather than rendered as a bar stuck at zero.
 */
export function parseDriveScanProgress(payload: unknown): DriveScanProgress | null {
    if (typeof payload !== 'object' || payload === null) return null;
    const update = payload as Record<string, unknown>;
    if (update.phase !== 'counting' && update.phase !== 'applying' && update.phase !== 'waiting') {
        return null;
    }
    return {
        phase: update.phase,
        pages_done: count(update.pages_done),
        pages_total: count(update.pages_total),
        messages_done: count(update.messages_done),
        messages_total: count(update.messages_total),
        wait_seconds: count(update.wait_seconds),
    };
}

export interface PersonalDriveErrorOptions {
    detail?: string;
    createRetry?: boolean;
}

const initialState = (): PersonalDriveSetupState => ({
    phase: 'loading',
    candidates: [],
    error: '',
    detail: '',
    createRetry: false,
    scan: null,
    waitSeconds: 0,
});

function copyCandidates(candidates: PersonalDriveCandidate[]): PersonalDriveCandidate[] {
    return candidates.map((candidate) => ({ ...candidate }));
}

function createPersonalDriveSetupStore() {
    const { subscribe, set, update } = writable<PersonalDriveSetupState>(initialState());

    return {
        subscribe,
        reset: () => set(initialState()),
        loading: () => set(initialState()),
        showCandidates: (candidates: PersonalDriveCandidate[]) => set({
            ...initialState(),
            phase: 'ready',
            candidates: copyCandidates(candidates),
        }),
        recovering: (options: { createRetry?: boolean } = {}) => update((state) => ({
            ...state,
            phase: 'recovering',
            error: '',
            detail: '',
            createRetry: options.createRetry ?? state.createRetry,
            scan: null,
            waitSeconds: 0,
        })),
        // Scans also run outside recovery (routine background syncs), so
        // updates are dropped unless a recovery is actually on screen. A
        // throttling pause annotates the last real progress rather than
        // replacing it, so the bar holds its position instead of resetting.
        scanProgress: (update_: DriveScanProgress) => update((state) => {
            if (state.phase !== 'recovering') return state;
            if (update_.phase === 'waiting') {
                return { ...state, waitSeconds: Math.max(0, update_.wait_seconds) };
            }
            return { ...state, scan: update_, waitSeconds: 0 };
        }),
        discoveryError: (error: string, detail = '') => set({
            ...initialState(),
            phase: 'discovery-error',
            error,
            detail,
        }),
        recoveryError: (error: string, options: PersonalDriveErrorOptions = {}) => update((state) => ({
            ...state,
            phase: 'ready',
            error,
            detail: options.detail ?? '',
            createRetry: options.createRetry ?? false,
        })),
    };
}

export const personalDriveSetup = createPersonalDriveSetupStore();
