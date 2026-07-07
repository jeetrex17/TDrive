import { writable } from 'svelte/store';

// Which auth screen is visible. null means no auth screen (the wrapper is
// hidden, or the dashboard is showing). modules/auth.ts drives this from the
// login flow and the Telegram event stream.
export type AuthScreen = 'setup' | 'phone' | 'code' | 'password' | null;

export const authScreen = writable<AuthScreen>(null);

// The submitted phone number, shown as the "Sent to" pill on the code screen.
export const authPhone = writable('');

// The 2FA password hint from Telegram; empty hides the hint row.
export const authHint = writable('');

// Bumped when Telegram rejects a login code, so the code screen clears and
// refocuses its field even though it stays on the same screen (a screen-value
// change cannot signal this because the value does not change).
export const authCodeReset = writable(0);
