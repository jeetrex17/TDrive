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
    /** Changes when notify replaces this id, unlike timer pause/resume updates. */
    revision?: number;
    level: ToastLevel;
    title: string;
    body: string;
    sticky: boolean;
    spinner: boolean;
    durationMs: number;
    // Absolute deadline for auto-dismiss. The nearest-
    // deadline scheduler in modules/notifications.ts owns this field together
    // with the paused/remainingMs pair that freezes hover time.
    expiresAt: number;
    paused: boolean;
    remainingMs?: number;
    action?: ToastAction;
    /** Called once when this entry leaves the visible stack for any reason. */
    onRemoved?: () => void;
}

export const toasts = writable<ToastItem[]>([]);
