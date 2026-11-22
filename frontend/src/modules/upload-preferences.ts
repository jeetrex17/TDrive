// Whether uploads to the personal drive are encrypted. The first upload asks;
// after that the answer is the default, changed from the account menu.

import { writable } from 'svelte/store';

export const ENCRYPT_UPLOADS_STORAGE_KEY = 'tdrive.uploads.encrypt.v1';

function readStored(): boolean | null {
    try {
        const value = window.localStorage.getItem(ENCRYPT_UPLOADS_STORAGE_KEY);
        return value === 'true' ? true : value === 'false' ? false : null;
    } catch {
        return null;
    }
}

/** null until the user has answered once. */
export const encryptUploads = writable<boolean | null>(readStored());

export function setEncryptUploads(encrypt: boolean): void {
    encryptUploads.set(encrypt);
    try {
        window.localStorage.setItem(ENCRYPT_UPLOADS_STORAGE_KEY, String(encrypt));
    } catch {
        // A private or full storage keeps the choice for this session only.
    }
}
