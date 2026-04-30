// Top-right avatar dropdown. Currently hosts the logout entry; future
// account/settings actions belong here too.

import { state } from '../state.js';
import { Me } from '../../wailsjs/go/main/App';
import { renderAvatar } from './avatar.js';
import { openLogoutModal } from './modals/logout.js';

let trigger = null;
let menu = null;
let outsideClickBound = false;

export function setupProfileMenu() {
    trigger = document.getElementById('profile-trigger');
    menu = document.getElementById('profile-menu');
    const logoutItem = document.getElementById('profile-menu-logout');
    if (!trigger || !menu || !logoutItem) return;

    renderAvatar(document.getElementById('profile-avatar'), null);

    trigger.addEventListener('click', (e) => {
        e.stopPropagation();
        toggleMenu();
    });

    document.addEventListener('keydown', (e) => {
        if (e.key === 'Escape' && isOpen()) {
            closeMenu();
            trigger.focus();
        }
    });

    logoutItem.addEventListener('click', () => {
        closeMenu();
        openLogoutModal();
    });
}

// loadSelfUser fetches the logged-in user once after dashboard mount and
// hydrates both avatars + the menu header. Called from auth.js after
// InitDrive succeeds. Failures fall back silently to initials/blank.
export async function loadSelfUser() {
    try {
        const user = await Me();
        state.selfUser = user || null;
    } catch (err) {
        console.warn('Me failed:', err);
        state.selfUser = null;
    }
    renderProfile();
}

function renderProfile() {
    const user = state.selfUser;
    renderAvatar(document.getElementById('profile-avatar'), user);
    renderAvatar(document.getElementById('profile-menu-avatar'), user);

    const name = document.getElementById('profile-menu-name');
    const handle = document.getElementById('profile-menu-handle');
    const phone = document.getElementById('profile-menu-phone');

    if (name) name.textContent = user?.display_name || 'Signed in';
    if (handle) {
        const u = String(user?.username || '').trim();
        handle.textContent = u ? `@${u}` : '';
        handle.style.display = u ? '' : 'none';
    }
    if (phone) {
        const p = String(user?.phone || '').trim();
        phone.textContent = p;
        phone.style.display = p ? '' : 'none';
    }
}

function toggleMenu() {
    if (isOpen()) closeMenu();
    else openMenu();
}

function openMenu() {
    if (!trigger || !menu) return;
    menu.hidden = false;
    trigger.setAttribute('aria-expanded', 'true');
    if (!outsideClickBound) {
        document.addEventListener('click', onDocumentClick, true);
        outsideClickBound = true;
    }
    const first = menu.querySelector('[role="menuitem"]');
    if (first) first.focus();
}

function closeMenu() {
    if (!trigger || !menu) return;
    menu.hidden = true;
    trigger.setAttribute('aria-expanded', 'false');
    if (outsideClickBound) {
        document.removeEventListener('click', onDocumentClick, true);
        outsideClickBound = false;
    }
}

function isOpen() {
    return menu && !menu.hidden;
}

function onDocumentClick(e) {
    if (!menu || !trigger) return;
    if (menu.contains(e.target) || trigger.contains(e.target)) return;
    closeMenu();
}
