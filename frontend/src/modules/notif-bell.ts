// Notification bell — the single unified feedback surface in the top-right
// header. Three layers of detail:
//
//   • Idle bell — quiet, dim. Nothing happening.
//   • Hover popover — only when transfers are active. Mini progress list.
//   • Click panel — full history. Active transfers + Recent (notifications
//     and completed transfers, merged chronologically).
//
// History lives in state.historyEvents (capped at 100, ephemeral). Modules
// elsewhere call `pushHistoryEvent(...)` and `pushTransferStart()` /
// `updateTransferProgress()` / `markTransferDone()` to feed the bell.

import { state } from '../state';

const HISTORY_CAP = 100;
const HOVER_GRACE_MS = 280;

let bellEl: HTMLElement | null = null;
let popoverEl: HTMLElement | null = null;
let panelEl: HTMLElement | null = null;
let hoverGraceTimer: any = null;
let pulseTickerHandle: any = null;

// ─── ICON LIBRARY ────────────────────────────────────────────────────────────

const ICONS = {
    bell: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M6 8a6 6 0 1112 0c0 7 3 9 3 9H3s3-2 3-9"/><path d="M10.3 21a1.94 1.94 0 003.4 0"/></svg>',
    upload: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 19V5"/><path d="M5 12l7-7 7 7"/></svg>',
    download: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 5v14"/><path d="M5 12l7 7 7-7"/></svg>',
    success: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><polyline points="20 6 9 17 4 12"/></svg>',
    error:   '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><line x1="15" y1="9" x2="9" y2="15"/><line x1="9" y1="9" x2="15" y2="15"/></svg>',
    info:    '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><line x1="12" y1="16" x2="12" y2="12"/><line x1="12" y1="8" x2="12.01" y2="8"/></svg>',
    warning: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z"/><line x1="12" y1="9" x2="12" y2="13"/><line x1="12" y1="17" x2="12.01" y2="17"/></svg>',
    cancel:  '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="10"/><line x1="8" y1="12" x2="16" y2="12"/></svg>',
};

// ─── PUBLIC API ──────────────────────────────────────────────────────────────

export function setupNotifBell() {
    bellEl = document.getElementById('notif-bell');
    if (!bellEl) return;

    bellEl.innerHTML = `<span class="notif-bell-icon" aria-hidden="true">${ICONS.bell}</span><span class="notif-bell-dot" data-mode="idle" aria-hidden="true"></span>`;
    bellEl.setAttribute('aria-haspopup', 'dialog');
    bellEl.setAttribute('aria-expanded', 'false');
    bellEl.setAttribute('aria-label', 'Notifications');

    bellEl.addEventListener('click', (e) => {
        e.stopPropagation();
        togglePanel();
    });
    bellEl.addEventListener('keydown', (e) => {
        if (e.key === 'Enter' || e.key === ' ') {
            e.preventDefault();
            togglePanel();
        }
    });

    bellEl.addEventListener('mouseenter', () => {
        if (state.notifPanelOpen) return;
        clearTimeout(hoverGraceTimer);
        if (hasActiveTransfers()) openHoverPopover();
    });
    bellEl.addEventListener('mouseleave', () => {
        clearTimeout(hoverGraceTimer);
        hoverGraceTimer = setTimeout(() => {
            if (popoverEl && !popoverEl.matches(':hover')) closeHoverPopover();
        }, HOVER_GRACE_MS);
    });

    // Outside-click and Esc close the panel.
    document.addEventListener('mousedown', (e) => {
        if (!state.notifPanelOpen) return;
        if (panelEl && (panelEl.contains(e.target as Node) || bellEl!.contains(e.target as Node))) return;
        closePanel();
    });
    window.addEventListener('keydown', (e) => {
        if (e.key !== 'Escape') return;
        if (state.notifPanelOpen) closePanel();
        else if (state.notifHoverOpen) closeHoverPopover();
    });

    renderBell();
    startPulseTicker();
}

// pushHistoryEvent enqueues a non-transfer event (folder created, drive
// joined, error, etc.). Returns the event id so callers can dedupe by
// reusing the same id, though that's rarely needed.
export function pushHistoryEvent({ level = 'info', title = '', body = '', ts }: { level?: string; title?: string; body?: string; ts?: number } = {}) {
    const id = `evt:${Date.now()}:${Math.random().toString(36).slice(2, 7)}`;
    state.historyEvents.unshift({
        kind: 'event',
        id,
        level,
        title: String(title || ''),
        body: String(body || ''),
        ts: ts || Date.now(),
    });
    if (level === 'error' && !state.notifPanelOpen) {
        state.notifUnreadErrors += 1;
    }
    capHistory();
    renderAll();
    return id;
}

// pushTransferStart begins tracking an upload or download. id should be
// unique per transfer (msg_id, upload id, etc.). id=0 is valid — upload
// IDs are zero-based, so don't reject falsy.
export function pushTransferStart({ id, direction, name, total = 0 }: any) {
    if (id == null || !direction) return;
    const key = `xfer:${direction}:${id}`;
    // De-dup: if an entry with this key already exists, replace it.
    removeFromHistory(key);
    state.historyEvents.unshift({
        kind: 'transfer',
        id: key,
        direction,
        name: String(name || ''),
        progress: 0,
        total: Number(total) || 0,
        status: 'active',
        startedAt: Date.now(),
        finishedAt: 0,
    });
    capHistory();
    renderAll();
    return key;
}

export function updateTransferProgress({ id, direction, progress }: any) {
    const key = `xfer:${direction}:${id}`;
    const entry = state.historyEvents.find((e) => e.id === key);
    if (!entry || entry.status !== 'active') return;
    const value = Math.max(0, Math.min(100, Number(progress) || 0));
    if (Math.round(entry.progress) === Math.round(value)) return; // skip noise
    entry.progress = value;
    renderAll();
}

export function markTransferDone({ id, direction, status = 'done' }: any) {
    const key = `xfer:${direction}:${id}`;
    const entry = state.historyEvents.find((e) => e.id === key);
    if (!entry) return;
    // Idempotent: don't downgrade or rewrite an already-terminal entry
    // (e.g. a safety sweep firing 'done' on an entry that already failed).
    if (entry.status !== 'active') return;
    entry.status = status;
    entry.progress = status === 'done' ? 100 : entry.progress;
    entry.finishedAt = Date.now();
    if (status === 'failed' && !state.notifPanelOpen) {
        state.notifUnreadErrors += 1;
    }
    renderAll();
}

export function clearHistory() {
    // Keep active transfers. Drop everything else.
    state.historyEvents = state.historyEvents.filter(
        (e) => e.kind === 'transfer' && e.status === 'active'
    );
    state.notifUnreadErrors = 0;
    renderAll();
}

// ─── INTERNAL: STATE ─────────────────────────────────────────────────────────

function capHistory() {
    if (state.historyEvents.length <= HISTORY_CAP) return;
    state.historyEvents = state.historyEvents.slice(0, HISTORY_CAP);
}

function removeFromHistory(id: any) {
    const idx = state.historyEvents.findIndex((e) => e.id === id);
    if (idx >= 0) state.historyEvents.splice(idx, 1);
}

function hasActiveTransfers() {
    return state.historyEvents.some((e) => e.kind === 'transfer' && e.status === 'active');
}

// ─── INTERNAL: RENDER ────────────────────────────────────────────────────────

function renderAll() {
    renderBell();
    if (state.notifHoverOpen) renderHoverPopover();
    if (state.notifPanelOpen) renderPanel();
}

function renderBell() {
    if (!bellEl) return;
    const dot = bellEl.querySelector('.notif-bell-dot') as HTMLElement | null;
    if (!dot) return;
    let mode = 'idle';
    if (state.notifUnreadErrors > 0) mode = 'error';
    else if (hasActiveTransfers()) mode = 'active';
    dot.dataset.mode = mode;
    bellEl.dataset.mode = mode;
}

function startPulseTicker() {
    if (pulseTickerHandle) cancelAnimationFrame(pulseTickerHandle);
    pulseTickerHandle = requestAnimationFrame(() => {
        pulseTickerHandle = null;
        renderBell();
    });
}

// ─── HOVER POPOVER ───────────────────────────────────────────────────────────

function openHoverPopover() {
    if (state.notifPanelOpen) return;
    if (!hasActiveTransfers()) return;
    state.notifHoverOpen = true;
    if (!popoverEl) {
        popoverEl = document.createElement('div');
        popoverEl.className = 'notif-popover';
        popoverEl.setAttribute('role', 'dialog');
        popoverEl.setAttribute('aria-label', 'Active transfers');
        popoverEl.addEventListener('mouseenter', () => clearTimeout(hoverGraceTimer));
        popoverEl.addEventListener('mouseleave', () => {
            clearTimeout(hoverGraceTimer);
            hoverGraceTimer = setTimeout(closeHoverPopover, HOVER_GRACE_MS);
        });
        popoverEl.addEventListener('click', (e) => {
            // Click into the popover → upgrade to full panel.
            e.stopPropagation();
            closeHoverPopover();
            openPanel();
        });
        document.body.appendChild(popoverEl);
    }
    positionAnchored(popoverEl);
    renderHoverPopover();
}

function closeHoverPopover() {
    state.notifHoverOpen = false;
    if (!popoverEl) return;
    popoverEl.classList.add('notif-leaving');
    const node = popoverEl;
    popoverEl = null;
    setTimeout(() => node.remove(), 160);
}

function renderHoverPopover() {
    if (!popoverEl) return;
    const active = state.historyEvents.filter(
        (e) => e.kind === 'transfer' && e.status === 'active'
    );
    if (active.length === 0) {
        closeHoverPopover();
        return;
    }
    popoverEl.innerHTML = `
        <div class="notif-popover-list">
            ${active.slice(0, 4).map(transferRowHTML).join('')}
        </div>
        ${active.length > 4 ? `<div class="notif-popover-more">+ ${active.length - 4} more</div>` : ''}
    `;
}

// ─── FULL PANEL ──────────────────────────────────────────────────────────────

function togglePanel() {
    if (state.notifPanelOpen) closePanel();
    else openPanel();
}

function openPanel() {
    closeHoverPopover();
    state.notifPanelOpen = true;
    state.notifUnreadErrors = 0; // opening clears the unread badge
    if (!panelEl) {
        panelEl = document.createElement('div');
        panelEl.className = 'notif-panel';
        panelEl.setAttribute('role', 'dialog');
        panelEl.setAttribute('aria-modal', 'false');
        panelEl.setAttribute('aria-label', 'Notifications');
        panelEl.addEventListener('click', (e) => e.stopPropagation());
        document.body.appendChild(panelEl);
    }
    positionAnchored(panelEl);
    renderPanel();
    bellEl?.setAttribute('aria-expanded', 'true');
    renderBell();
}

function closePanel() {
    state.notifPanelOpen = false;
    bellEl?.setAttribute('aria-expanded', 'false');
    if (!panelEl) return;
    panelEl.classList.add('notif-leaving');
    const node = panelEl;
    panelEl = null;
    setTimeout(() => node.remove(), 160);
    renderBell();
}

function renderPanel() {
    if (!panelEl) return;
    const active = state.historyEvents.filter(
        (e) => e.kind === 'transfer' && e.status === 'active'
    );
    const recent = state.historyEvents.filter(
        (e) => !(e.kind === 'transfer' && e.status === 'active')
    );

    panelEl.innerHTML = `
        <div class="notif-panel-header">
            <div class="notif-panel-title">Notifications</div>
            <button class="notif-panel-clear" type="button" ${recent.length === 0 ? 'disabled' : ''}>Clear</button>
        </div>
        <div class="notif-panel-body">
            ${active.length > 0 ? `
                <div class="notif-section-label">Active</div>
                <div class="notif-section">
                    ${active.map(transferRowHTML).join('')}
                </div>
            ` : ''}
            ${recent.length > 0 ? `
                <div class="notif-section-label" style="margin-top:${active.length > 0 ? '14px' : '0'}">Recent</div>
                <div class="notif-section">
                    ${recent.slice(0, 50).map(eventRowHTML).join('')}
                </div>
            ` : (active.length === 0 ? `
                <div class="notif-empty">
                    <div class="notif-empty-glyph">${ICONS.bell}</div>
                    <div class="notif-empty-title">All caught up</div>
                    <div class="notif-empty-body">Folder activity, uploads, and shared-drive events will show up here.</div>
                </div>
            ` : '')}
        </div>
    `;
    const clearBtn = panelEl.querySelector('.notif-panel-clear');
    if (clearBtn) clearBtn.addEventListener('click', clearHistory);

    // Click an error row to copy its body to clipboard.
    panelEl.querySelectorAll('.notif-row[data-clipable="1"]').forEach((row: any) => {
        row.addEventListener('click', async () => {
            const text = row.dataset.copyText || '';
            if (!text) return;
            try { await navigator.clipboard.writeText(text); } catch {}
            row.classList.add('notif-copied');
            setTimeout(() => row.classList.remove('notif-copied'), 800);
        });
    });
}

// ─── ROW TEMPLATES ───────────────────────────────────────────────────────────

function transferRowHTML(t: any) {
    const direction = t.direction === 'up' ? 'upload' : 'download';
    const dirLabel = t.direction === 'up' ? 'Uploading' : 'Downloading';
    const statusClass =
        t.status === 'done' ? 'is-done' :
        t.status === 'failed' ? 'is-failed' :
        t.status === 'canceled' ? 'is-canceled' :
        'is-active';
    const meta =
        t.status === 'done' ? 'Done' :
        t.status === 'failed' ? 'Failed' :
        t.status === 'canceled' ? 'Canceled' :
        `${Math.round(t.progress || 0)}%`;
    return `
        <div class="notif-row notif-row-transfer ${statusClass}">
            <span class="notif-row-icon" data-kind="${direction}" aria-hidden="true">${ICONS[direction]}</span>
            <div class="notif-row-body">
                <div class="notif-row-title" title="${escapeHTML(t.name)}">${escapeHTML(t.name || dirLabel)}</div>
                <div class="notif-row-progress">
                    <div class="notif-row-progress-fill" style="width:${Math.max(0, Math.min(100, t.progress || 0))}%"></div>
                </div>
            </div>
            <div class="notif-row-meta">${escapeHTML(meta)}</div>
        </div>
    `;
}

function eventRowHTML(e: any) {
    if (e.kind === 'transfer') {
        // Completed/failed transfer in Recent — render compactly.
        return transferRowHTML(e);
    }
    const level = e.level || 'info';
    const icon = ICONS[level as keyof typeof ICONS] || ICONS.info;
    const clipable = level === 'error' && e.body ? '1' : '0';
    const copyText = clipable === '1' ? `${e.title}\n${e.body}` : '';
    return `
        <div class="notif-row notif-row-event level-${level}"
             data-clipable="${clipable}" data-copy-text="${escapeHTML(copyText)}">
            <span class="notif-row-icon" data-kind="${level}" aria-hidden="true">${icon}</span>
            <div class="notif-row-body">
                <div class="notif-row-title">${escapeHTML(e.title)}</div>
                ${e.body ? `<div class="notif-row-sub">${escapeHTML(e.body)}</div>` : ''}
            </div>
            <div class="notif-row-meta">${escapeHTML(formatRelative(e.ts))}</div>
        </div>
    `;
}

// ─── POSITIONING ─────────────────────────────────────────────────────────────

function positionAnchored(node: any) {
    if (!bellEl || !node) return;
    const rect = bellEl.getBoundingClientRect();
    const right = Math.max(12, window.innerWidth - rect.right);
    const top = rect.bottom + 10;
    node.style.right = `${right}px`;
    node.style.top = `${top}px`;
}

// ─── UTILS ───────────────────────────────────────────────────────────────────

function escapeHTML(s: any) {
    return String(s).replace(/[&<>"']/g, (c) => ({
        '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
    } as Record<string, string>)[c]);
}

function formatRelative(ts: any) {
    const t = Number(ts);
    if (!t) return '';
    const diffSec = Math.floor((Date.now() - t) / 1000);
    if (diffSec < 30) return 'just now';
    if (diffSec < 60) return `${diffSec}s ago`;
    const m = Math.floor(diffSec / 60);
    if (m < 60) return `${m} min ago`;
    const h = Math.floor(m / 60);
    if (h < 24) return `${h} hr ago`;
    const d = Math.floor(h / 24);
    if (d < 7) return `${d}d ago`;
    return new Date(t).toLocaleDateString();
}

// Reposition open surfaces on viewport resize.
window.addEventListener('resize', () => {
    if (popoverEl) positionAnchored(popoverEl);
    if (panelEl) positionAnchored(panelEl);
});
