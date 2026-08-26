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
    error: string;
    createRetry: boolean;
}

const initialState = (): PersonalDriveSetupState => ({
    phase: 'loading',
    candidates: [],
    error: '',
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
            phase: 'ready',
            candidates: copyCandidates(candidates),
            error: '',
            createRetry: false,
        }),
        recovering: (options: { createRetry?: boolean } = {}) => update((state) => ({
            ...state,
            phase: 'recovering',
            error: '',
            createRetry: options.createRetry ?? state.createRetry,
        })),
        discoveryError: (error: string) => set({
            phase: 'discovery-error',
            candidates: [],
            error,
            createRetry: false,
        }),
        recoveryError: (error: string, createRetry = false) => update((state) => ({
            ...state,
            phase: 'ready',
            error,
            createRetry,
        })),
    };
}

export const personalDriveSetup = createPersonalDriveSetupStore();
