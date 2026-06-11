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

import { state } from '../state';
import { pushHistoryEvent } from './notif-bell';

const MAX_VISIBLE = 5;
const DEFAULT_DURATION = 4000;
const ICONS = {
    info: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="10"/><line x1="12" y1="16" x2="12" y2="12"/><line x1="12" y1="8" x2="12.01" y2="8"/></svg>',
    success: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="20 6 9 17 4 12"/></svg>',
    warning: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z"/><line x1="12" y1="9" x2="12" y2="13"/><line x1="12" y1="17" x2="12.01" y2="17"/></svg>',
    error: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="10"/><line x1="15" y1="9" x2="9" y2="15"/><line x1="9" y1="9" x2="15" y2="15"/></svg>',
    spinner: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" class="toast-spinner"><circle cx="12" cy="12" r="9" stroke-opacity="0.25"/><path d="M12 3 a9 9 0 0 1 9 9"/></svg>',
};

let stackEl: HTMLElement | null = null;
let timer: number | null = null;

export function setupNotifications() {
    if (stackEl) return;
    stackEl = document.createElement('div');
    stackEl.id = 'toast-stack';
    stackEl.className = 'toast-stack';
    stackEl.setAttribute('role', 'status');
    stackEl.setAttribute('aria-live', 'polite');
    document.body.appendChild(stackEl);

    // Pause auto-dismiss while the stack is hovered (per-toast pausing
    // happens via the data attribute too; this is the broad gesture).
    stackEl.addEventListener('mouseenter', () => setAllPaused(true));
    stackEl.addEventListener('mouseleave', () => setAllPaused(false));

    // Esc clears the most recent error toast (sticky errors otherwise
    // require a manual click).
    window.addEventListener('keydown', (e) => {
        if (e.key !== 'Escape') return;
        const lastError = [...state.toasts].reverse().find((t) => t.level === 'error');
        if (lastError) dismissNotification(lastError.id);
    });

    ensureTimer();
}

// notify enqueues a toast. Returns its id; pass the same id back via
// `notify({ id })` to replace an existing entry in place (used for
// long-running operations).
export function notify(opts: any = {}) {
    const level = ['info', 'success', 'warning', 'error'].includes(opts.level) ? opts.level : 'info';
    const id = opts.id || `t${Date.now()}-${Math.random().toString(36).slice(2, 7)}`;
    const sticky = opts.sticky === true || level === 'error' || opts.durationMs === 0;
    const duration = sticky ? 0 : (Number.isFinite(opts.durationMs) ? opts.durationMs : DEFAULT_DURATION);
    const now = Date.now();
    const entry = {
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

    // Mirror non-spinner / non-progress toasts into the bell history. We
    // skip in-progress sticky toasts (spinners) because their final
    // success/failure version replaces them; the panel doesn't need both.
    const isProgress = entry.spinner === true;
    if (!isProgress && entry.title) {
        pushHistoryEvent({
            level: entry.level,
            title: entry.title,
            body: entry.body,
            ts: now,
        });
    }

    const idx = state.toasts.findIndex((t) => t.id === id);
    if (idx >= 0) {
        // Preserve element identity if the level is unchanged, so the
        // replacement morphs in place instead of slide-in/slide-out.
        const existing = state.toasts[idx];
        const morph = existing.level === level;
        state.toasts[idx] = entry;
        renderStack({ keepNode: morph ? id : null });
    } else {
        // Cap the visible queue; if exceeded, the oldest non-sticky entry
        // is dismissed early so urgent ones aren't drowned.
        if (state.toasts.length >= MAX_VISIBLE) {
            const stalest = state.toasts.findIndex((t) => !t.sticky);
            if (stalest >= 0) state.toasts.splice(stalest, 1);
            else state.toasts.shift();
        }
        state.toasts.push(entry);
        renderStack();
    }
    ensureTimer();
    return id;
}

export function dismissNotification(id: any) {
    const idx = state.toasts.findIndex((t) => t.id === id);
    if (idx < 0) return;
    state.toasts.splice(idx, 1);
    renderStack();
    ensureTimer();
}

export function clearAllNotifications() {
    state.toasts = [];
    renderStack();
    if (timer) {
        cancelAnimationFrame(timer);
        timer = null;
    }
}

function setAllPaused(paused: boolean) {
    if (!state.toasts.length) return;
    const now = Date.now();
    for (const t of state.toasts) {
        if (t.sticky) continue;
        if (paused && !t.paused) {
            t.paused = true;
            t.remainingMs = Math.max(0, (t.expiresAt || now) - now);
        } else if (!paused && t.paused) {
            t.paused = false;
            t.expiresAt = now + (t.remainingMs || 0);
        }
    }
    ensureTimer();
}

function hasExpiringToasts() {
    return state.toasts.some((t) => !t.sticky && !t.paused && t.expiresAt);
}

function ensureTimer() {
    if (timer || !hasExpiringToasts()) return;
    timer = requestAnimationFrame(tick);
}

function tick() {
    timer = null;
    const now = Date.now();
    let changed = false;
    for (let i = state.toasts.length - 1; i >= 0; i--) {
        const t = state.toasts[i];
        if (t.sticky || t.paused) continue;
        if (t.expiresAt && now >= t.expiresAt) {
            state.toasts.splice(i, 1);
            changed = true;
        }
    }
    if (changed) renderStack();
    ensureTimer();
}

function renderStack({ keepNode = null } = {}) {
    if (!stackEl) return;

    // Build a quick map of existing nodes by id so we can preserve
    // identity for entries that didn't change (avoids reflow / animation
    // restart on every render).
    const existing = new Map();
    for (const node of stackEl.querySelectorAll('.toast')) {
        existing.set((node as HTMLElement).dataset.id, node);
    }

    const fragment = document.createDocumentFragment();
    for (const t of state.toasts) {
        let node = existing.get(t.id);
        if (node && (t.id !== keepNode)) {
            // Re-attach existing node if the underlying entry hasn't
            // structurally changed. We always rebuild content for safety
            // (cheap and keeps title/body in sync if a notify() call
            // updated them).
        }
        if (!node) {
            node = buildToastNode(t);
        } else {
            updateToastNode(node, t);
        }
        existing.delete(t.id);
        fragment.appendChild(node);
    }

    // Animate-out any nodes whose entries were removed.
    for (const stale of existing.values()) {
        stale.classList.add('toast-leaving');
        // After the CSS transition (~180ms) drop it from the DOM.
        stale.addEventListener('transitionend', () => stale.remove(), { once: true });
        // Safety net in case transitionend doesn't fire.
        setTimeout(() => { if (stale.isConnected) stale.remove(); }, 240);
    }

    stackEl.appendChild(fragment);
}

function buildToastNode(t: any) {
    const node = document.createElement('div');
    node.className = `toast toast-${t.level}`;
    node.dataset.id = t.id;
    node.setAttribute('role', t.level === 'error' ? 'alert' : 'status');
    node.setAttribute('tabindex', '0');

    node.addEventListener('mouseenter', () => { t.paused = true; });
    node.addEventListener('mouseleave', () => {
        if (!t.sticky && t.paused) {
            t.paused = false;
            // resume from where we paused — use the remaining time stored
            // on entry; fall back to a fresh timeout if absent.
            const remaining = t.remainingMs || t.durationMs || DEFAULT_DURATION;
            t.expiresAt = Date.now() + remaining;
        }
    });

    updateToastNode(node, t);
    return node;
}

function updateToastNode(node: HTMLElement, t: any) {
    const iconKey = (t.spinner ? 'spinner' : t.level) as keyof typeof ICONS;
    node.innerHTML = `
        <span class="toast-icon" aria-hidden="true">${ICONS[iconKey] || ICONS.info}</span>
        <div class="toast-content">
            <div class="toast-title">${escapeHTML(t.title)}</div>
            ${t.body ? `<div class="toast-body">${escapeHTML(t.body)}</div>` : ''}
        </div>
        <button class="toast-close" type="button" aria-label="Dismiss">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
        </button>
    `;
    const closeBtn = node.querySelector('.toast-close');
    if (closeBtn) {
        closeBtn.addEventListener('click', (e) => {
            e.stopPropagation();
            dismissNotification(t.id);
        });
    }
    // Click body to dismiss errors (lets users clear them quickly without
    // hunting for the X).
    node.addEventListener('click', (e) => {
        if ((e.target as HTMLElement).closest('.toast-close')) return;
        if (t.level === 'error') dismissNotification(t.id);
    });
}

function escapeHTML(s: any) {
    return String(s).replace(/[&<>"']/g, (c) => ({
        '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
    } as Record<string, string>)[c]);
}
