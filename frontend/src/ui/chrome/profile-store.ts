import { writable } from 'svelte/store';
import type { AvatarUser } from '../../modules/avatar';

export type ProfileUser = AvatarUser;

// null means "not loaded yet"; the menu shows its loading header until the
// first typed profile request resolves or fails.
export const profileUser = writable<ProfileUser | null>(null);
export const profileLoaded = writable(false);
export const encryptionEntryVisible = writable(false);
