// Admin modal for approval-required Telegram invite links.

import { approveJoinRequest, listJoinRequests, rejectJoinRequest } from '../channels.js';
import { notify } from '../notifications.js';

let activeDrive = null;

export function setupJoinRequestsModal() {
    const modal = document.getElementById('join-requests-modal');
    const close = document.getElementById('join-requests-close');
    if (!modal || !close) return;

    const dismiss = () => {
        modal.style.display = 'none';
        activeDrive = null;
    };

    close.addEventListener('click', dismiss);
    modal.addEventListener('click', (e) => { if (e.target === modal) dismiss(); });
}

export async function openJoinRequestsModal(drive) {
    const modal = document.getElementById('join-requests-modal');
    const subtitle = document.getElementById('join-requests-subtitle');
    const list = document.getElementById('join-requests-list');
    if (!modal || !list) return;

    activeDrive = { id: Number(drive?.id || 0), title: String(drive?.title || 'this drive') };
    if (!activeDrive.id) return;

    if (subtitle) subtitle.textContent = `Pending requests for ${activeDrive.title}.`;
    modal.style.display = 'flex';
    await renderJoinRequests();
}

async function renderJoinRequests() {
    const list = document.getElementById('join-requests-list');
    if (!list || !activeDrive?.id) return;

    list.innerHTML = '<div class="modal-empty">Loading requests...</div>';
    let rows = [];
    try {
        rows = await listJoinRequests(activeDrive.id);
    } catch (err) {
        list.innerHTML = '';
        const el = document.createElement('div');
        el.className = 'modal-error';
        el.style.display = 'block';
        el.textContent = `Failed to load requests: ${err}`;
        list.appendChild(el);
        return;
    }

    list.innerHTML = '';
    if (!Array.isArray(rows) || rows.length === 0) {
        const empty = document.createElement('div');
        empty.className = 'modal-empty';
        empty.textContent = 'No pending requests.';
        list.appendChild(empty);
        return;
    }

    for (const req of rows) {
        list.appendChild(joinRequestRow(req));
    }
}

function joinRequestRow(req) {
    const row = document.createElement('div');
    row.className = 'join-request-row';

    const meta = document.createElement('div');
    meta.className = 'join-request-meta';

    const name = document.createElement('div');
    name.className = 'join-request-name';
    name.textContent = String(req?.display_name || `User ${req?.user_id || ''}`).trim();

    const detail = document.createElement('div');
    detail.className = 'join-request-detail';
    const pieces = [];
    if (req?.username) pieces.push(String(req.username));
    if (req?.requested_at) pieces.push(new Date(Number(req.requested_at) * 1000).toLocaleString());
    detail.textContent = pieces.join(' · ');

    meta.appendChild(name);
    meta.appendChild(detail);

    const actions = document.createElement('div');
    actions.className = 'join-request-actions';

    const approve = document.createElement('button');
    approve.type = 'button';
    approve.className = 'secondary-btn compact-btn';
    approve.textContent = 'Approve';
    approve.addEventListener('click', () => handleRequest(req, true, approve, reject));

    const reject = document.createElement('button');
    reject.type = 'button';
    reject.className = 'secondary-btn compact-btn danger-text';
    reject.textContent = 'Reject';
    reject.addEventListener('click', () => handleRequest(req, false, approve, reject));

    actions.appendChild(approve);
    actions.appendChild(reject);

    row.appendChild(meta);
    row.appendChild(actions);
    return row;
}

async function handleRequest(req, approved, approveBtn, rejectBtn) {
    if (!activeDrive?.id || !req?.user_id) return;
    approveBtn.disabled = true;
    rejectBtn.disabled = true;
    try {
        if (approved) {
            await approveJoinRequest(activeDrive.id, req.user_id);
        } else {
            await rejectJoinRequest(activeDrive.id, req.user_id);
        }
        await renderJoinRequests();
    } catch (err) {
        notify({
            level: 'error',
            title: `Could not ${approved ? 'approve' : 'reject'} request`,
            body: String(err),
        });
        approveBtn.disabled = false;
        rejectBtn.disabled = false;
    }
}
