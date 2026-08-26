import { derived, writable } from 'svelte/store';
import {
    DEFAULT_UPDATE_PREFS,
    UPDATE_PREFS_STORAGE_KEY,
    initialUpdateState,
    menuBadge,
    parseUpdatePrefs,
    serializeUpdatePrefs,
    type AppVersionInfo,
    type UpdatePrefs,
    type UpdateState,
} from './update-model';

// The single source of truth for the updater UI. modules/updates.ts pushes
// backend snapshots in; components read the derived views out.
export const updateState = writable<UpdateState>(initialUpdateState());
export const appVersionInfo = writable<AppVersionInfo | null>(null);

function readInitialPrefs(): UpdatePrefs {
    if (typeof window === 'undefined') return { ...DEFAULT_UPDATE_PREFS };
    try {
        return parseUpdatePrefs(window.localStorage.getItem(UPDATE_PREFS_STORAGE_KEY));
    } catch {
        return { ...DEFAULT_UPDATE_PREFS };
    }
}

export const updatePrefs = writable<UpdatePrefs>(readInitialPrefs());

if (typeof window !== 'undefined') {
    updatePrefs.subscribe((prefs) => {
        try {
            window.localStorage.setItem(UPDATE_PREFS_STORAGE_KEY, serializeUpdatePrefs(prefs));
        } catch {
            // Preferences are a convenience; the updater still works unsaved.
        }
    });
}

// Nonce bumped to ask the account menu to open straight to the update view
// (the macOS "Check for Updates…" item, the login-screen link).
export const updatesPanelRequest = writable(0);

export function requestUpdatesPanel(): void {
    updatesPanelRequest.update((n) => n + 1);
}

export const updateBadge = derived(updateState, menuBadge);

export function setAutoDownload(enabled: boolean): void {
    updatePrefs.update((prefs) => ({ ...prefs, autoDownload: enabled }));
}

export function skipVersion(version: string): void {
    if (!version) return;
    updatePrefs.update((prefs) => ({ ...prefs, skippedVersion: version }));
}

export function clearSkippedVersion(): void {
    updatePrefs.update((prefs) => ({ ...prefs, skippedVersion: '' }));
}
