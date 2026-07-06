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
//   notify({ level: 'error', title: 'Could not join drive', body: String(err) });
//
// This module owns the queue: capping, replace-by-id, and the expiry ticker
// with its hover-pause rules. ToastStack.svelte only renders the store.

import { get } from 'svelte/store';
import { pushHistoryEvent } from './notif-bell';
import ToastStack from '../ui/notifications/ToastStack.svelte';
import { toasts, type ToastItem, type ToastLevel } from '../ui/notifications/toast-store';
import { mountSvelte, type SvelteMountHandle } from '../ui/mount';

const MAX_VISIBLE = 5;
const DEFAULT_DURATION = 4000;
const LEVELS: readonly ToastLevel[] = ['info', 'success', 'warning', 'error'];

let stackHandle: SvelteMountHandle<Record<string, unknown>> | null = null;
let timer: number | null = null;

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

    ensureTimer();
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
    const entry: ToastItem = {
        id,
        level,
        title: String(opts.title || ''),
        body: opts.body ? String(opts.body) : '',
        sticky,
        durationMs: duration,
        expiresAt: duration > 0 ? now + duration : 0,
        paused: false,
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
            next.splice(stalest >= 0 ? stalest : 0, 1);
        }
        next.push(entry);
        return next;
    });
    ensureTimer();
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
    ensureTimer();
}

export function clearAllNotifications() {
    toasts.set([]);
    if (timer) {
        cancelAnimationFrame(timer);
        timer = null;
    }
}

// pauseToast freezes one toast's countdown while it is hovered; resumeToast
// restarts it from the captured remainder (or a fresh window when the broad
// stack-level pause didn't capture one).
function pauseToast(id: string) {
    toasts.update((list) =>
        list.map((t) => (t.id === id && !t.paused ? { ...t, paused: true } : t)),
    );
}

function resumeToast(id: string) {
    const now = Date.now();
    toasts.update((list) =>
        list.map((t) => {
            if (t.id !== id || t.sticky || !t.paused) return t;
            const remaining = t.remainingMs || t.durationMs || DEFAULT_DURATION;
            return { ...t, paused: false, expiresAt: now + remaining };
        }),
    );
    ensureTimer();
}

function setAllPaused(paused: boolean) {
    const now = Date.now();
    toasts.update((list) => {
        if (!list.length) return list;
        return list.map((t) => {
            if (t.sticky) return t;
            if (paused && !t.paused) {
                return { ...t, paused: true, remainingMs: Math.max(0, (t.expiresAt || now) - now) };
            }
            if (!paused && t.paused) {
                return { ...t, paused: false, expiresAt: now + (t.remainingMs || 0) };
            }
            return t;
        });
    });
    ensureTimer();
}

function hasExpiringToasts() {
    return get(toasts).some((t) => !t.sticky && !t.paused && t.expiresAt);
}

function ensureTimer() {
    if (timer || !hasExpiringToasts()) return;
    timer = requestAnimationFrame(tick);
}

function tick() {
    timer = null;
    const now = Date.now();
    // The rAF loop runs every frame while a countdown is live; only touch the
    // store (and wake its subscribers) when something actually expired.
    const survives = (t: ToastItem) => t.sticky || t.paused || !t.expiresAt || now < t.expiresAt;
    if (!get(toasts).every(survives)) {
        toasts.update((list) => list.filter(survives));
    }
    ensureTimer();
}
