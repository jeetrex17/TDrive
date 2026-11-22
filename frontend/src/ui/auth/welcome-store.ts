import { writable } from 'svelte/store';

/**
 * Whether the reader has moved past the welcome page in this run. The welcome
 * is presentation only: the auth state machine stays on 'setup' underneath it,
 * and this flag decides which of the two pages that screen shows.
 */
export const welcomeSeen = writable(false);
