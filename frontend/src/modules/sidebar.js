// Sidebar — renders the list of drives (personal + shared) and routes
// clicks to channels.js. Re-renders on every loadChannels() call.

import { state } from '../state.js';
import {
    switchActiveChannel,
    getInviteLink,
    getApprovalInviteLink,
    checkPendingJoin,
    removePendingJoin,
} from './channels.js';
import { openShareDriveModal } from './modals/share-drive.js';
import { openLeaveDriveModal } from './modals/leave-drive.js';
import { openNewDriveModal } from './modals/new-drive.js';
import { openJoinDriveModal } from './modals/join-drive.js';
import { openJoinRequestsModal } from './modals/join-requests.js';
import { notify } from './notifications.js';

let personalEl = null;
let sharedEl = null;

export function setupSidebar() {
    personalEl = document.getElementById('drives-personal');
    sharedEl = document.getElementById('drives-shared');

    const newBtn = document.getElementById('open-new-drive');
    if (newBtn) newBtn.addEventListener('click', () => openNewDriveModal());
    const joinBtn = document.getElementById('open-join-drive');
    if (joinBtn) joinBtn.addEventListener('click', () => openJoinDriveModal());

    renderSidebar();
}

export function renderSidebar() {
    if (!personalEl || !sharedEl) return;

    const channels = state.channels || [];
    const personal = channels.filter((c) => c?.kind === 'personal');
    const shared = channels.filter((c) => c?.kind === 'shared');
    const pending = Array.isArray(state.pendingJoins) ? state.pendingJoins : [];

    personalEl.innerHTML = '';
    if (personal.length === 0) {
        personalEl.appendChild(emptyRow('Loading...'));
    } else {
        for (const c of personal) personalEl.appendChild(driveRow(c, false));
    }

    sharedEl.innerHTML = '';
    if (shared.length === 0 && pending.length === 0) {
        sharedEl.appendChild(emptyRow('No shared drives yet'));
    } else {
        for (const c of shared) sharedEl.appendChild(driveRow(c, true));
        for (const p of pending) sharedEl.appendChild(pendingJoinRow(p));
    }
}

function emptyRow(label) {
    const el = document.createElement('div');
    el.className = 'drive-empty';
    el.textContent = label;
    return el;
}

function driveRow(c, isShared) {
    const row = document.createElement('button');
    row.type = 'button';
    row.className = 'drive-item';
    if (c.is_active) row.classList.add('active');
    row.dataset.channelId = String(c.id);
    row.title = c.title;
    row.innerHTML = `
        <svg class="icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            ${isShared
                ? '<path stroke-linecap="round" stroke-linejoin="round" d="M17 20h5v-2a4 4 0 00-3-3.87M9 20H4v-2a4 4 0 013-3.87m6-5a4 4 0 11-8 0 4 4 0 018 0zm6 0a4 4 0 11-8 0 4 4 0 018 0z"/>'
                : '<path stroke-linecap="round" stroke-linejoin="round" d="M3 7a2 2 0 012-2h5l2 2h7a2 2 0 012 2v8a2 2 0 01-2 2H5a2 2 0 01-2-2V7z"/>'}
        </svg>
        <span class="drive-item-title"></span>
    `;
    row.querySelector('.drive-item-title').textContent = c.title || 'Untitled';

    row.addEventListener('click', () => {
        if (Number(c.id) === Number(state.activeChannel?.id)) return;
        switchActiveChannel(Number(c.id));
    });

    if (isShared) {
        row.addEventListener('contextmenu', (e) => {
            e.preventDefault();
            showSharedContextMenu(e, c);
        });
    }

    return row;
}

function pendingJoinRow(p) {
    const row = document.createElement('button');
    row.type = 'button';
    row.className = 'drive-item pending-drive-item';
    row.dataset.inviteHash = String(p.invite_hash || '');
    const title = String(p.title || 'Pending request');
    const error = String(p.last_error || '');
    row.title = error ? `Waiting for approval — ${error}` : 'Waiting for admin approval';
    row.innerHTML = `
        <svg class="icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M12 8v4l3 2m6-2a9 9 0 11-18 0 9 9 0 0118 0z"/>
        </svg>
        <span class="drive-item-title"></span>
        <span class="pending-drive-tag">pending</span>
    `;
    row.querySelector('.drive-item-title').textContent = title;

    row.addEventListener('click', async () => {
        try {
            const result = await checkPendingJoin(String(p.invite_hash || ''));
            if (result?.status === 'joined') {
                notify({
                    level: 'success',
                    title: 'Request approved',
                    body: 'Joined the drive.',
                });
            } else {
                notify({
                    level: 'info',
                    title: 'Still waiting for approval',
                });
            }
        } catch (err) {
            notify({
                level: 'error',
                title: 'Could not check request',
                body: String(err),
            });
        }
    });

    row.addEventListener('contextmenu', (e) => {
        e.preventDefault();
        showPendingContextMenu(e, p);
    });

    return row;
}

function showSharedContextMenu(event, c) {
    const menu = document.getElementById('context-menu');
    if (!menu) return;
    menu.innerHTML = '';
    menu.style.display = 'block';
    menu.style.top = `${event.clientY}px`;
    menu.style.left = `${event.clientX}px`;

    // Append a <button> rather than <div>: the global .context-menu CSS
    // styles its button children, so this matches the file-list right-click
    // menu instead of rendering as bare text.
    const addItem = (label, fn) => {
        const btn = document.createElement('button');
        btn.type = 'button';
        btn.textContent = label;
        btn.addEventListener('click', () => {
            menu.style.display = 'none';
            fn();
        });
        menu.appendChild(btn);
    };

    addItem('Copy invite link', async () => {
        try {
            const link = await getInviteLink(Number(c.id));
            openShareDriveModal(link, { approvalRequired: false });
        } catch (err) {
            notify({
                level: 'error',
                title: 'Could not get invite link',
                body: String(err),
            });
        }
    });
    addItem('Copy approval link', async () => {
        try {
            const link = await getApprovalInviteLink(Number(c.id));
            openShareDriveModal(link, { approvalRequired: true });
        } catch (err) {
            notify({
                level: 'error',
                title: 'Could not get approval link',
                body: String(err),
            });
        }
    });
    addItem('Join requests', () => {
        openJoinRequestsModal({ id: Number(c.id), title: c.title });
    });
    addItem('Leave drive', () => {
        openLeaveDriveModal({ id: Number(c.id), title: c.title });
    });

    const close = (e) => {
        if (!menu.contains(e.target)) {
            menu.style.display = 'none';
            document.removeEventListener('click', close);
        }
    };
    setTimeout(() => document.addEventListener('click', close), 0);
}

function showPendingContextMenu(event, p) {
    const menu = document.getElementById('context-menu');
    if (!menu) return;
    menu.innerHTML = '';
    menu.style.display = 'block';
    menu.style.top = `${event.clientY}px`;
    menu.style.left = `${event.clientX}px`;

    const addItem = (label, fn, className = '') => {
        const btn = document.createElement('button');
        btn.type = 'button';
        btn.textContent = label;
        if (className) btn.className = className;
        btn.addEventListener('click', () => {
            menu.style.display = 'none';
            fn();
        });
        menu.appendChild(btn);
    };

    addItem('Check now', async () => {
        try {
            const result = await checkPendingJoin(String(p.invite_hash || ''));
            notify({
                level: result?.status === 'joined' ? 'success' : 'info',
                title: result?.status === 'joined' ? 'Request approved' : 'Still waiting for approval',
                body: result?.status === 'joined' ? 'Joined the drive.' : '',
            });
        } catch (err) {
            notify({
                level: 'error',
                title: 'Could not check request',
                body: String(err),
            });
        }
    });
    addItem('Remove request', async () => {
        try {
            await removePendingJoin(String(p.invite_hash || ''));
            notify({
                level: 'success',
                title: 'Pending request removed',
            });
        } catch (err) {
            notify({
                level: 'error',
                title: 'Could not remove request',
                body: String(err),
            });
        }
    }, 'danger');

    const close = (e) => {
        if (!menu.contains(e.target)) {
            menu.style.display = 'none';
            document.removeEventListener('click', close);
        }
    };
    setTimeout(() => document.addEventListener('click', close), 0);
}
