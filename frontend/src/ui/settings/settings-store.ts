import { writable } from 'svelte/store';

/** The sections of the desktop Settings window, in sidebar order. */
export type SettingsSection = 'account' | 'appearance' | 'backup' | 'encryption' | 'mount' | 'updates' | 'language';

export interface SettingsView {
    open: boolean;
    section: SettingsSection;
}

export const settingsView = writable<SettingsView>({ open: false, section: 'account' });

/** Opens Settings, on a given section or on the one last shown. */
export function openSettings(section?: SettingsSection): void {
    settingsView.update((view) => ({ open: true, section: section ?? view.section }));
}

export function showSettingsSection(section: SettingsSection): void {
    settingsView.update((view) => ({ ...view, section }));
}

export function closeSettings(): void {
    settingsView.update((view) => ({ ...view, open: false }));
}
