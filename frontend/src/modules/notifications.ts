// Toast notification system. Single global stack rendered in the
// bottom-right corner. Replaces every alert() and the #status-msg pill.
//
// Usage:
//   import { notify, dismissNotification } from './notifications';
//
//   // Transient info / success (auto-dismiss after ~4s):
//   notify({ level: 'success', title: 'Folder created' });
//
//   // Sticky in-progress with id, then replace on resolve:
//   notify({ id: 'creating', level: 'info', title: 'Creating folder…',
//            sticky: true });
//   await thing();
//   dismissNotification('creating');
//   notify({ level: 'success', title: 'Folder created' });
//
//   // Errors are sticky by default; user dismisses or clicks to copy.
//   notify({ level: 'error', title: 'Could not join drive', body: 'Try again.' });
//
// This module owns the queue: capping, replace-by-id, and the nearest-deadline
// expiry scheduler with its hover-pause rules. ToastStack.svelte only renders
// the store.

import { get } from 'svelte/store';
import { pushHistoryEvent } from './notif-bell';
import ToastStack from '../ui/notifications/ToastStack.svelte';
import { toasts, type ToastItem, type ToastLevel } from '../ui/notifications/toast-store';
import { mountSvelte, type SvelteMountHandle } from '../ui/mount';

const MAX_VISIBLE = 5;
const DEFAULT_DURATION = 4000;
const MAX_TIMEOUT_DELAY_MS = 2_147_483_647;
const LEVELS: readonly ToastLevel[] = ['info', 'success', 'warning', 'error'];

let stackHandle: SvelteMountHandle<Record<string, unknown>> | null = null;
let expiryTimer: ReturnType<typeof setTimeout> | null = null;
let allPaused = false;
const individuallyPaused = new Set<string>();

export function setupNotifications() {
    if (stackHandle) return;

    const stackEl = document.createElement('div');
    stackEl.id = 'toast-stack';
    stackEl.className = 'toast-stack';
    stackEl.setAttribute('role', 'status');
    stackEl.setAttribute('aria-live', 'polite');
    document.body.appendChild(stackEl);

    stackHandle = mountSvelte(ToastStack, {
        target: stackEl,
        props: {
            onDismiss: dismissNotification,
            onPauseToast: pauseToast,
            onResumeToast: resumeToast,
            onPauseAll: () => setAllPaused(true),
            onResumeAll: () => setAllPaused(false),
        },
    });

    // Esc clears the most recent error toast (sticky errors otherwise
    // require a manual click). A modal's own Escape handling runs in the
    // capture phase and stops propagation, so this never fires behind one.
    window.addEventListener('keydown', (e) => {
        if (e.key !== 'Escape') return;
        const lastError = [...get(toasts)].reverse().find((t) => t.level === 'error');
        if (lastError) dismissNotification(lastError.id);
    });

    // A suspended WebView can wake long after a timeout's deadline. Reconcile
    // against wall-clock time immediately instead of waiting for a clamped
    // background timer to run.
    document.addEventListener('visibilitychange', handleAppWake);
    window.addEventListener('focus', handleAppWake);
    window.addEventListener('pageshow', handleAppWake);

    rescheduleExpiry();
}

// notify enqueues a toast. Returns its id; pass the same id back via
// `notify({ id })` to replace an existing entry in place (used for
// long-running operations).
export function notify(opts: any = {}) {
    const level: ToastLevel = LEVELS.includes(opts.level) ? opts.level : 'info';
    const id = opts.id || `t${Date.now()}-${Math.random().toString(36).slice(2, 7)}`;
    const sticky = opts.sticky === true || level === 'error' || opts.durationMs === 0;
    const duration = sticky ? 0 : (Number.isFinite(opts.durationMs) ? opts.durationMs : DEFAULT_DURATION);
    const now = Date.now();
    const paused = allPaused || individuallyPaused.has(id);
    const entry: ToastItem = {
        id,
        level,
        title: String(opts.title || ''),
        body: opts.body ? String(opts.body) : '',
        sticky,
        durationMs: duration,
        expiresAt: duration > 0 ? now + duration : 0,
        paused,
        ...(paused && duration > 0 ? { remainingMs: duration } : {}),
        spinner: opts.spinner === true,
    };

    // Mirror non-spinner toasts into the bell history. In-progress sticky
    // toasts (spinners) are skipped because their final success/failure
    // version replaces them; the panel doesn't need both.
    if (!entry.spinner && entry.title) {
        pushHistoryEvent({
            level: entry.level,
            title: entry.title,
            body: entry.body,
            ts: now,
        });
    }

    let evictedID = '';
    toasts.update((list) => {
        const idx = list.findIndex((t) => t.id === id);
        if (idx >= 0) {
            // Replace in place; the keyed each block morphs the same node.
            const next = [...list];
            next[idx] = entry;
            return next;
        }
        // Cap the visible queue; if exceeded, the oldest non-sticky entry
        // is dismissed early so urgent ones aren't drowned.
        const next = [...list];
        if (next.length >= MAX_VISIBLE) {
            const stalest = next.findIndex((t) => !t.sticky);
            [evictedID] = next.splice(stalest >= 0 ? stalest : 0, 1).map((toast) => toast.id);
        }
        next.push(entry);
        return next;
    });
    if (evictedID) individuallyPaused.delete(evictedID);
    rescheduleExpiry();
    return id;
}

export function dismissNotification(id: string) {
    toasts.update((list) => {
        const idx = list.findIndex((t) => t.id === id);
        if (idx < 0) return list;
        const next = [...list];
        next.splice(idx, 1);
        return next;
    });
    individuallyPaused.delete(id);
    rescheduleExpiry();
}

export function clearAllNotifications() {
    toasts.set([]);
    individuallyPaused.clear();
    allPaused = false;
    clearExpiryTimer();
}

// pauseToast freezes one toast's countdown while it is hovered. Stack and
// toast hover states are tracked separately so moving between child toasts
// cannot accidentally restart a countdown while the stack remains hovered.
function pauseToast(id: string) {
    individuallyPaused.add(id);
    const now = Date.now();
    toasts.update((list) => list.map((toast) => {
        if (toast.id !== id || toast.sticky || toast.paused) return toast;
        return pauseAt(toast, now);
    }));
    rescheduleExpiry();
}

function resumeToast(id: string) {
    individuallyPaused.delete(id);
    if (allPaused) {
        rescheduleExpiry();
        return;
    }
    const now = Date.now();
    toasts.update((list) => list.map((toast) => {
        if (toast.id !== id || toast.sticky || !toast.paused) return toast;
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
            if (toast.sticky) return toast;
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

function expireDueToasts(now: number) {
    const survives = (toast: ToastItem) => (
        toast.sticky || toast.paused || !toast.expiresAt || now < toast.expiresAt
    );
    if (!get(toasts).every(survives)) {
        toasts.update((list) => {
            for (const toast of list) {
                if (!survives(toast)) individuallyPaused.delete(toast.id);
            }
            return list.filter(survives);
        });
    }
}

function nearestDeadline(): number | null {
    let nearest = Infinity;
    for (const toast of get(toasts)) {
        if (toast.sticky || toast.paused || !toast.expiresAt) continue;
        nearest = Math.min(nearest, toast.expiresAt);
    }
    return Number.isFinite(nearest) ? nearest : null;
}

function rescheduleExpiry() {
    clearExpiryTimer();
    expireDueToasts(Date.now());
    scheduleNearestDeadline();
}

function scheduleNearestDeadline() {
    const deadline = nearestDeadline();
    if (deadline === null) return;
    const delay = Math.min(MAX_TIMEOUT_DELAY_MS, Math.max(0, deadline - Date.now()));
    expiryTimer = setTimeout(handleExpiryTimer, delay);
}

function handleExpiryTimer() {
    expiryTimer = null;
    expireDueToasts(Date.now());
    scheduleNearestDeadline();
}
