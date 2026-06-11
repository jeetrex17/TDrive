// Top-right avatar dropdown. Currently hosts the logout entry; future
// account/settings actions belong here too.

import { state } from '../state';
import { Me } from '../../wailsjs/go/main/App';
import { renderAvatar } from './avatar';
import { openLogoutModal } from './modals/logout';
import { openEncryptionSettingsModal } from './modals/encryption-settings';

let trigger: HTMLElement | null = null;
let menu: HTMLElement | null = null;
let outsideClickBound = false;
let selfUserPromise: Promise<any> | null = null;

export function setupProfileMenu() {
    trigger = document.getElementById('profile-trigger');
    menu = document.getElementById('profile-menu');
    const logoutItem = document.getElementById('profile-menu-logout');
    const encryptionItem = document.getElementById('profile-menu-encryption-settings');
    if (!trigger || !menu || !logoutItem) return;

    renderProfileLoading();

    trigger.addEventListener('click', (e) => {
        e.stopPropagation();
        toggleMenu();
    });

    document.addEventListener('keydown', (e) => {
        if (e.key === 'Escape' && isOpen()) {
            closeMenu();
            trigger!.focus();
        }
    });

    logoutItem.addEventListener('click', () => {
        closeMenu();
        openLogoutModal();
    });
    if (encryptionItem) {
        encryptionItem.addEventListener('click', () => {
            closeMenu();
            openEncryptionSettingsModal();
        });
    }

    renderEncryptionSettingsEntry();
}

// loadSelfUser fetches the logged-in user once after dashboard mount and
// hydrates both avatars + the menu header. Called from auth.js after
// InitDrive succeeds. Failures fall back silently to initials/blank.
export async function loadSelfUser() {
    if (selfUserPromise) return selfUserPromise;
    selfUserPromise = (async () => {
        try {
            const user = await Me();
            state.selfUser = user || null;
        } catch (err) {
            console.warn('Me failed:', err);
            state.selfUser = null;
        }
        renderProfile();
        selfUserPromise = null;
        return state.selfUser;
    })();
    return selfUserPromise;
}

function renderProfileLoading() {
    renderAvatar(document.getElementById('profile-avatar'), null);
    renderAvatar(document.getElementById('profile-menu-avatar'), null);
    const name = document.getElementById('profile-menu-name');
    const handle = document.getElementById('profile-menu-handle');
    if (name) name.textContent = 'Loading account…';
    if (handle) {
        handle.textContent = '';
        handle.style.display = 'none';
    }
}

async function ensureProfileLoaded() {
    if (state.selfUser) return;
    renderProfileLoading();
    try {
        await loadSelfUser();
    } catch {}
}

function renderProfile() {
    const user = state.selfUser;
    renderAvatar(document.getElementById('profile-avatar'), user);
    renderAvatar(document.getElementById('profile-menu-avatar'), user);

    const name = document.getElementById('profile-menu-name');
    const handle = document.getElementById('profile-menu-handle');

    if (name) name.textContent = user?.display_name || 'Telegram account';
    if (handle) {
        const u = String(user?.username || '').trim();
        handle.textContent = u ? `@${u}` : '';
        handle.style.display = u ? '' : 'none';
    }
}

export function renderEncryptionSettingsEntry() {
    const item = document.getElementById('profile-menu-encryption-settings');
    const divider = document.getElementById('profile-menu-encryption-settings-divider');
    const show = !!state.encryption?.passwordSet;
    if (item) item.hidden = !show;
    if (divider) divider.hidden = !show;
}

function toggleMenu() {
    if (isOpen()) closeMenu();
    else openMenu();
}

function openMenu() {
    if (!trigger || !menu) return;
    menu.hidden = false;
    trigger.setAttribute('aria-expanded', 'true');
    ensureProfileLoaded();
    if (!outsideClickBound) {
        document.addEventListener('click', onDocumentClick, true);
        outsideClickBound = true;
    }
    const first = menu.querySelector('[role="menuitem"]') as HTMLElement | null;
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

function onDocumentClick(e: MouseEvent) {
    if (!menu || !trigger) return;
    if (menu.contains(e.target as Node) || trigger.contains(e.target as Node)) return;
    closeMenu();
}
