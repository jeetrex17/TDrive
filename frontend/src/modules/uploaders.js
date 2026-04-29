// Uploader chip helpers. Resolves Telegram user IDs to display names
// lazily via the backend, caches in state.userNames, and formats relative
// timestamps for the chip suffix.

import { state } from '../state.js';
import { ResolveUsernames } from '../../wailsjs/go/main/App';

// Resolve a batch of uploader IDs not already in the cache. Safe to call
// repeatedly — duplicates and pre-cached IDs are filtered. Returns a
// Promise that resolves once the cache reflects the request.
//
// On failure: existing cache entries are kept; missing IDs stay missing,
// and the chip render path falls back to "no chip" for those rows.
export async function ensureUserNames(userIDs) {
    const missing = [];
    const waitFor = [];
    const seen = new Set();
    for (const id of userIDs) {
        const n = Number(id);
        if (!Number.isFinite(n) || n <= 0) continue;
        const key = String(n);
        if (seen.has(key)) continue;
        seen.add(key);
        if (state.userNames.has(key)) continue;
        if (state.userNameFailures.has(key)) continue;
        if (state.userNameRequests.has(key)) {
            waitFor.push(state.userNameRequests.get(key));
            continue;
        }
        missing.push(n);
    }

    let request = null;
    if (missing.length > 0) {
        request = ResolveUsernames(missing).then((resolved) => {
            const got = new Set();
            if (resolved && typeof resolved === 'object') {
                for (const [k, v] of Object.entries(resolved)) {
                    state.userNames.set(String(k), String(v));
                    got.add(String(k));
                }
            }
            missing.forEach((id) => {
                const key = String(id);
                if (!got.has(key)) state.userNameFailures.add(key);
            });
        }).catch((err) => {
            console.warn('ResolveUsernames failed:', err);
            missing.forEach((id) => state.userNameFailures.add(String(id)));
        }).finally(() => {
            missing.forEach((id) => state.userNameRequests.delete(String(id)));
        });
        missing.forEach((id) => state.userNameRequests.set(String(id), request));
        waitFor.push(request);
    }

    if (waitFor.length === 0) return;
    await Promise.allSettled(waitFor);
}

export async function populateUploaderChips(list) {
    if (!list) return;
    if (state.activeChannel?.kind !== 'shared') return;

    const slots = list.querySelectorAll('[data-uploader-slot]');
    const ids = [];
    slots.forEach((slot) => {
        const row = slot.closest('.drive-row');
        if (!row) return;
        const uid = Number(row.dataset.uploaderId || 0);
        if (uid > 0) ids.push(uid);
    });
    if (ids.length === 0) return;

    await ensureUserNames(ids);

    slots.forEach((slot) => {
        const row = slot.closest('.drive-row');
        if (!row) return;
        const uid = Number(row.dataset.uploaderId || 0);
        const ts = Number(row.dataset.uploadTime || 0);
        const html = uploaderChipHTML({ uploaderID: uid, uploadTime: ts });
        slot.innerHTML = html ?? '';
    });
}

// formatRelative produces "2h ago" / "5m ago" / "just now" strings from
// a unix-second timestamp. Falls back to the empty string when ts is 0
// or invalid; callers should hide the time portion in that case.
export function formatRelative(unixSec) {
    const sec = Number(unixSec);
    if (!Number.isFinite(sec) || sec <= 0) return '';
    const diff = Math.floor(Date.now() / 1000) - sec;
    if (diff < 30) return 'just now';
    if (diff < 60) return `${diff}s ago`;
    const m = Math.floor(diff / 60);
    if (m < 60) return `${m}m ago`;
    const h = Math.floor(m / 60);
    if (h < 24) return `${h}h ago`;
    const d = Math.floor(h / 24);
    if (d < 7) return `${d}d ago`;
    const w = Math.floor(d / 7);
    if (w < 4) return `${w}w ago`;
    const mo = Math.floor(d / 30);
    if (mo < 12) return `${mo}mo ago`;
    const y = Math.floor(d / 365);
    return `${y}y ago`;
}

// Build the inner HTML of an uploader chip for a given file row, or
// return null if no chip should be shown.
//
// Visibility rules locked in Step 6:
//   - Shared drives only.
//   - uploaderID > 0 (zero = pre-Step-3 backfill row, no signal).
//   - Resolution succeeded (cached entry present).
//
// Self users render as "You · 2h ago" — surfacing your own files is
// useful in shared drives where multiple people contribute.
export function uploaderChipHTML(file) {
    if (!file) return null;
    if (state.activeChannel?.kind !== 'shared') return null;
    const uploaderID = Number(file.uploaderID ?? file.uploader_id ?? 0);
    if (uploaderID <= 0) return null;
    const name = state.userNames.get(String(uploaderID));
    if (!name) return null;
    const when = formatRelative(file.uploadTime ?? file.upload_time ?? file.date ?? 0);
    const suffix = when ? ` · ${when}` : '';
    return escapeForChip(name) + escapeForChip(suffix);
}

function escapeForChip(s) {
    return String(s).replace(/[&<>"']/g, (c) => ({
        '&': '&amp;',
        '<': '&lt;',
        '>': '&gt;',
        '"': '&quot;',
        "'": '&#39;',
    })[c]);
}
