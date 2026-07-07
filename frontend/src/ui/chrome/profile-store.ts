import { writable } from 'svelte/store';
import type { AvatarUser } from '../../modules/avatar';

export interface ProfileUser extends AvatarUser {
    display_name?: string;
    username?: string;
}

// null means "not loaded yet"; the menu shows its loading header until the
// first Me() resolves (or fails, which falls back to the generic label).
export const profileUser = writable<ProfileUser | null>(null);
export const profileLoaded = writable(false);
export const encryptionEntryVisible = writable(false);
