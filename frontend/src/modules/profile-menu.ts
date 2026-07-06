// Top-right avatar dropdown. Currently hosts the logout entry; future
// account/settings actions belong here too.

import { get } from 'svelte/store';
import { state } from '../state';
import { Me } from '../../wailsjs/go/main/App';
import { openLogoutModal } from './modals/logout';
import { openEncryptionSettingsModal } from './modals/encryption-settings';
import ProfileMenu from '../ui/chrome/ProfileMenu.svelte';
import {
    encryptionEntryVisible,
    profileLoaded,
    profileUser,
    type ProfileUser,
} from '../ui/chrome/profile-store';
import { mountSvelte, type SvelteMountHandle } from '../ui/mount';

let profileMenuHandle: SvelteMountHandle<Record<string, unknown>> | null = null;
let selfUserPromise: Promise<ProfileUser | null> | null = null;

export function setupProfileMenu() {
    const host = document.getElementById('profile-root');
    if (!host || profileMenuHandle) return;

    host.replaceChildren();
    profileMenuHandle = mountSvelte(ProfileMenu, {
        target: host,
        props: {
            onOpen: () => {
                void ensureProfileLoaded();
            },
            onEncryptionSettings: openEncryptionSettingsModal,
            onLogout: openLogoutModal,
        },
    });

    renderEncryptionSettingsEntry();
}

// loadSelfUser fetches the logged-in user once after dashboard mount and
// hydrates both avatars + the menu header. Called from auth.ts after
// InitDrive succeeds. Failures fall back silently to initials/blank.
export async function loadSelfUser(): Promise<ProfileUser | null> {
    if (selfUserPromise) return selfUserPromise;
    selfUserPromise = (async () => {
        let user: ProfileUser | null = null;
        try {
            user = ((await Me()) as ProfileUser) || null;
        } catch (err) {
            console.warn('Me failed:', err);
        }
        profileUser.set(user);
        profileLoaded.set(true);
        selfUserPromise = null;
        return user;
    })();
    return selfUserPromise;
}

async function ensureProfileLoaded(): Promise<void> {
    if (get(profileUser)) return; // already hydrated; menu opens use the cache
    await loadSelfUser();
}

export function renderEncryptionSettingsEntry() {
    encryptionEntryVisible.set(Boolean(state.encryption?.passwordSet));
}
