import { writable } from 'svelte/store';
import type { AuthScreen } from '../app/app-store';

export type AuthFlow = Exclude<AuthScreen, 'drive'>;

export interface AuthSubmissionStatus {
    busy: boolean;
    error: string;
}

export type AuthSubmissionState = Record<AuthFlow, AuthSubmissionStatus>;

function initialSubmissionState(): AuthSubmissionState {
    return {
        setup: { busy: false, error: '' },
        phone: { busy: false, error: '' },
        code: { busy: false, error: '' },
        password: { busy: false, error: '' },
    };
}


// The submitted phone number, shown as the "Sent to" pill on the code screen.
export const authPhone = writable('');

// The 2FA password hint from Telegram; empty hides the hint row.
export const authHint = writable('');

// Request state lives outside the component because code/password submissions
// complete through Telegram events after the Wails call itself has returned.
// Keeping one status per flow prevents an event from making another form look
// busy or overwriting its inline error.
export const authSubmission = writable<AuthSubmissionState>(initialSubmissionState());

/** Atomically starts a request. False means that flow already has one in flight. */
export function beginAuthSubmission(flow: AuthFlow): boolean {
    let started = false;
    authSubmission.update((state) => {
        if (state[flow].busy) return state;
        started = true;
        return {
            ...state,
            [flow]: { busy: true, error: '' },
        };
    });
    return started;
}

export function finishAuthSubmission(flow: AuthFlow): void {
    authSubmission.update((state) => ({
        ...state,
        [flow]: { busy: false, error: '' },
    }));
}

export function failAuthSubmission(flow: AuthFlow, error: string): void {
    authSubmission.update((state) => ({
        ...state,
        [flow]: { busy: false, error },
    }));
}

export function resetAuthSubmissions(): void {
    authSubmission.set(initialSubmissionState());
}
