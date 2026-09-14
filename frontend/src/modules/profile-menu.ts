// Top-right avatar dropdown. Currently hosts the logout entry; future
// account/settings actions belong here too.

import { get } from 'svelte/store';
import { getSelfUser } from '../api';
import { profileLoaded, profileUser, type ProfileUser } from '../ui/chrome/profile-store';
let selfUserPromise: Promise<ProfileUser | null> | null = null;


// loadSelfUser fetches the logged-in user once after dashboard mount and
// hydrates both avatars + the menu header. Called from auth.ts after
// InitDrive succeeds. Failures fall back silently to initials/blank.
export async function loadSelfUser(): Promise<ProfileUser | null> {
    if (selfUserPromise) return selfUserPromise;
    selfUserPromise = (async () => {
        let user: ProfileUser | null = null;
        try {
            user = await getSelfUser();
        } catch (err) {
            console.warn('Profile load failed:', err);
        }
        profileUser.set(user);
        profileLoaded.set(true);
        selfUserPromise = null;
        return user;
    })();
    return selfUserPromise;
}

export async function ensureProfileLoaded(): Promise<void> {
    if (get(profileUser)) return; // already hydrated; menu opens use the cache
    await loadSelfUser();
}
