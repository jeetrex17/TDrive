import { writable } from 'svelte/store';

export type ToastLevel = 'info' | 'success' | 'warning' | 'error';

/**
 * One inline button. It is deliberately not mirrored into the bell history:
 * history is a log of what happened, and a button there would act on state
 * that has long since moved on.
 */
export interface ToastAction {
    label: string;
    run: () => void;
}

export interface ToastItem {
    id: string;
    level: ToastLevel;
    title: string;
    body: string;
    sticky: boolean;
    spinner: boolean;
    durationMs: number;
    // Absolute deadline for auto-dismiss; 0 for sticky toasts. The nearest-
    // deadline scheduler in modules/notifications.ts owns this field together
    // with the paused/remainingMs pair that freezes hover time.
    expiresAt: number;
    paused: boolean;
    remainingMs?: number;
    action?: ToastAction;
}

export const toasts = writable<ToastItem[]>([]);
