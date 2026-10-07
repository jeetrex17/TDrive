// Toast notification system. Single global stack rendered above app content.
// Replaces every alert() and the #status-msg pill.
//
// Usage:
//   import { notify, dismissNotification } from './notifications';
//
//   // Transient info / success (auto-dismiss after ~4s):
//   notify({ level: 'success', title: 'Folder created' });
//
//   // Timed in-progress with id, then replace on resolve:
//   notify({ id: 'creating', level: 'info', title: 'Creating folder…', spinner: true });
//   await thing();
//   dismissNotification('creating');
//   notify({ level: 'success', title: 'Folder created' });
//
//   // Errors stay longer than confirmations. All toasts expire.
//   notify({ level: 'error', title: 'Could not join drive', body: 'Try again.' });
//
// This module owns the queue: capping, replace-by-id, and the nearest-deadline
// expiry scheduler with its hover-pause rules. ToastStack.svelte only renders
// the store.

import { get } from 'svelte/store';
import { isMobilePlatform } from '../api';
import { toAppError, type AppErrorSource } from './errors';
import { pushHistoryEvent } from './notif-bell';
import { toasts, type ToastAction, type ToastItem, type ToastLevel } from '../ui/notifications/toast-store';

const MAX_VISIBLE = 3;
// Keep the phone stack small enough to leave app content visible.
const MAX_VISIBLE_MOBILE = 2;
function visibleCap(): number {
    return isMobilePlatform() ? MAX_VISIBLE_MOBILE : MAX_VISIBLE;
}
/**
 * How long each level stays, in ms. A failure takes longer to read than a
 * confirmation -- it has a reason attached and a decision behind it -- so it is
 * given the time rather than being pinned to the screen.
 *
 * Nothing here is permanent. Durable records live in notification history;
 * these notices only announce changes while the user is in the app.
 */
const LEVEL_DURATION: Record<ToastLevel, number> = {
    info: 4000,
    success: 4000,
    warning: 6000,
    error: 8000,
};
/** For a toast already on screen whose own duration has been lost. */
const DEFAULT_DURATION = LEVEL_DURATION.info;
/** A toast with a button has to outlast the glance that finds the button. */
const ACTIONABLE_DURATION = 8000;
const MAX_TIMEOUT_DELAY_MS = 2_147_483_647;
const LEVELS: readonly ToastLevel[] = ['info', 'success', 'warning', 'error'];

let expiryTimer: ReturnType<typeof setTimeout> | null = null;
let nextToastRevision = 0;
let allPaused = false;
const individuallyPaused = new Set<string>();

function handleToastEscape(event: KeyboardEvent): void {
    if (event.key !== 'Escape') return;
    const lastError = [...get(toasts)].reverse().find((toast) => toast.level === 'error');
    if (lastError) dismissNotification(lastError.id);
}

export function activateNotificationEffects(): () => void {
    window.addEventListener('keydown', handleToastEscape);
    document.addEventListener('visibilitychange', handleAppWake);
    window.addEventListener('focus', handleAppWake);
    window.addEventListener('pageshow', handleAppWake);
    rescheduleExpiry();

    return () => {
        window.removeEventListener('keydown', handleToastEscape);
        document.removeEventListener('visibilitychange', handleAppWake);
        window.removeEventListener('focus', handleAppWake);
        window.removeEventListener('pageshow', handleAppWake);
        clearExpiryTimer();
    };
}

export function pauseAllNotifications(): void {
    setAllPaused(true);
}

export function resumeAllNotifications(): void {
    setAllPaused(false);
}

// notify enqueues a toast. Returns its id; pass the same id back via
// `notify({ id })` to replace an existing entry in place (used for
// long-running operations).
export interface NotifyOptions {
    id?: string;
    level?: ToastLevel;
    title?: string;
    body?: string;
    /** Legacy option; all notices expire, including former sticky notices. */
    sticky?: boolean;
    durationMs?: number;
    spinner?: boolean;
    action?: ToastAction;
    /** Runs when the toast expires, is dismissed, replaced, evicted or cleared. */
    onRemoved?: () => void;
    /**
     * False for a toast that only repeats what a transfer row already says.
     * The row carries the reason as its note, so mirroring the toast into the
     * bell would list one failure twice and count it twice.
     */
    history?: boolean;
}

export function notify(opts: NotifyOptions = {}): string {
    const level: ToastLevel = opts.level && LEVELS.includes(opts.level) ? opts.level : 'info';
    const id = opts.id || `t${Date.now()}-${Math.random().toString(36).slice(2, 7)}`;
    const action = opts.action?.label && typeof opts.action.run === 'function'
        ? { label: String(opts.action.label), run: opts.action.run }
        : undefined;
    const requested = typeof opts.durationMs === 'number' && Number.isFinite(opts.durationMs) && opts.durationMs > 0
        ? opts.durationMs
        : null;
    const duration = Math.max(requested ?? LEVEL_DURATION[level], action ? ACTIONABLE_DURATION : 0);
    const now = Date.now();
    const paused = allPaused || individuallyPaused.has(id);
    const entry: ToastItem = {
        id,
        revision: ++nextToastRevision,
        level,
        title: String(opts.title || ''),
        body: opts.body ? String(opts.body) : '',
        sticky: false,
        durationMs: duration,
        expiresAt: now + duration,
        paused,
        ...(paused ? { remainingMs: duration } : {}),
        spinner: opts.spinner === true,
        onRemoved: opts.onRemoved,
        ...(action ? { action } : {}),
    };

    // A progress spinner is followed by a final result. Store only that result
    // in the bell so an operation does not appear twice in history.
    if (!entry.spinner && entry.title && opts.history !== false) {
        pushHistoryEvent({
            level: entry.level,
            title: entry.title,
            body: entry.body,
            ts: now,
        });
    }

    let removed: ToastItem | undefined;
    let evicted = false;
    toasts.update((list) => {
        const idx = list.findIndex((toast) => toast.id === id);
        if (idx >= 0) {
            // Keep a replacement in the same spot for a stable stack.
            const next = [...list];
            removed = next[idx];
            next[idx] = entry;
            return next;
        }
        // The oldest visible entry gives way; its history remains available.
        const next = [...list];
        if (next.length >= visibleCap()) {
            [removed] = next.splice(0, 1);
            evicted = true;
        }
        next.push(entry);
        return next;
    });
    if (evicted && removed) individuallyPaused.delete(removed.id);
    rescheduleExpiry();
    removed?.onRemoved?.();
    return id;
}
export interface AppErrorNotificationOptions {
    id?: string;
    title?: string;
    source?: AppErrorSource;
}

/**
 * Reports only normalized copy and contains notification-renderer failures.
 *
 * The error boundary may call this when a region stops working. The notice
 * expires after the standard error interval; the failure remains in history.
 */
export function notifyAppError(error: unknown, options: AppErrorNotificationOptions = {}): string | null {
    const appError = toAppError(error, { source: options.source });
    try {
        return notify({
            id: options.id,
            level: 'error',
            title: options.title ?? appError.title,
            body: appError.message,
        });
    } catch {
        return null;
    }
}

export function dismissNotification(id: string) {
    let removed: ToastItem | undefined;
    toasts.update((list) => {
        const idx = list.findIndex((t) => t.id === id);
        if (idx < 0) return list;
        const next = [...list];
        [removed] = next.splice(idx, 1);
        return next;
    });
    individuallyPaused.delete(id);
    rescheduleExpiry();
    removed?.onRemoved?.();
}

export function clearAllNotifications() {
    const removed = get(toasts);
    toasts.set([]);
    individuallyPaused.clear();
    allPaused = false;
    clearExpiryTimer();
    for (const toast of removed) toast.onRemoved?.();
}

// pauseToast freezes one toast's countdown while it is hovered. Stack and
// toast hover states are tracked separately so moving between child toasts
// cannot accidentally restart a countdown while the stack remains hovered.
export function pauseToast(id: string): void {
    individuallyPaused.add(id);
    const now = Date.now();
    toasts.update((list) => list.map((toast) => {
        if (toast.id !== id || toast.paused) return toast;
        return pauseAt(toast, now);
    }));
    rescheduleExpiry();
}

export function resumeToast(id: string): void {
    individuallyPaused.delete(id);
    if (allPaused) {
        rescheduleExpiry();
        return;
    }
    const now = Date.now();
    toasts.update((list) => list.map((toast) => {
        if (toast.id !== id || !toast.paused) return toast;
        const remaining = toast.remainingMs ?? toast.durationMs ?? DEFAULT_DURATION;
        return { ...toast, paused: false, expiresAt: now + remaining };
    }));
    rescheduleExpiry();
}

function setAllPaused(paused: boolean) {
    if (paused === allPaused) {
        rescheduleExpiry();
        return;
    }
    allPaused = paused;
    const now = Date.now();
    toasts.update((list) => {
        if (!list.length) return list;
        return list.map((toast) => {
            if (paused && !toast.paused) return pauseAt(toast, now);
            if (!paused && toast.paused && !individuallyPaused.has(toast.id)) {
                return { ...toast, paused: false, expiresAt: now + (toast.remainingMs ?? 0) };
            }
            return toast;
        });
    });
    rescheduleExpiry();
}

function pauseAt(toast: ToastItem, now: number): ToastItem {
    return {
        ...toast,
        paused: true,
        remainingMs: Math.max(0, (toast.expiresAt || now) - now),
    };
}

function handleAppWake() {
    if (document.visibilityState === 'hidden') return;
    rescheduleExpiry();
}

function clearExpiryTimer() {
    if (expiryTimer === null) return;
    clearTimeout(expiryTimer);
    expiryTimer = null;
}

function expireDueToasts(now: number): ToastItem[] {
    const removed: ToastItem[] = [];
    const survives = (toast: ToastItem) => (
        toast.paused || now < toast.expiresAt
    );
    if (!get(toasts).every(survives)) {
        toasts.update((list) => {
            for (const toast of list) {
                if (!survives(toast)) {
                    individuallyPaused.delete(toast.id);
                    removed.push(toast);
                }
            }
            return list.filter(survives);
        });
    }
    return removed;
}

function nearestDeadline(): number | null {
    let nearest = Infinity;
    for (const toast of get(toasts)) {
        if (toast.paused) continue;
        nearest = Math.min(nearest, toast.expiresAt);
    }
    return Number.isFinite(nearest) ? nearest : null;
}

function rescheduleExpiry() {
    clearExpiryTimer();
    const removed = expireDueToasts(Date.now());
    scheduleNearestDeadline();
    for (const toast of removed) toast.onRemoved?.();
}

function scheduleNearestDeadline() {
    const deadline = nearestDeadline();
    if (deadline === null) return;
    const delay = Math.min(MAX_TIMEOUT_DELAY_MS, Math.max(0, deadline - Date.now()));
    expiryTimer = setTimeout(handleExpiryTimer, delay);
}

function handleExpiryTimer() {
    expiryTimer = null;
    const removed = expireDueToasts(Date.now());
    scheduleNearestDeadline();
    for (const toast of removed) toast.onRemoved?.();
}
