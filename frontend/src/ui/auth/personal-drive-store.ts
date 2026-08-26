import { writable } from 'svelte/store';

export type PersonalDrivePhase = 'loading' | 'ready' | 'recovering' | 'discovery-error';

export interface PersonalDriveCandidate {
    id: string;
    title: string;
    created_at: number;
    has_activity: boolean;
    recommended: boolean;
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
        })),
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
