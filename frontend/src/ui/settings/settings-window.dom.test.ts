import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, tick, unmount } from 'svelte';
import { get } from 'svelte/store';

vi.mock('../../modules/updates', () => ({ checkForUpdates: vi.fn(async () => undefined) }));

import { checkForUpdates } from '../../modules/updates';
import SettingsWindow from './SettingsWindow.svelte';
import { openSettings, settingsView } from './settings-store';
import { setPreferredTheme, setThemeMode, THEME_TRANSITION_CLASS, themeController } from '../theme/theme-controller';

let component: Record<string, unknown> | null = null;
let host: HTMLElement | null = null;

function setup(props: { updaterAvailable?: boolean } = {}) {
    const onEncryptionSettings = vi.fn();
    const onLogout = vi.fn();
    // ModalShell shows and hides its overlay host by id, as FeatureLayer's are.
    host = document.createElement('div');
    host.id = 'settings-modal';
    host.style.display = 'none';
    document.body.appendChild(host);
    component = mount(SettingsWindow, { target: host, props: { onEncryptionSettings, onLogout, ...props } });
    flushSync();
    return { onEncryptionSettings, onLogout };
}

async function settle(): Promise<void> {
    flushSync();
    await tick();
    await Promise.resolve();
}

function rect(left: number, top: number, width: number, height: number): DOMRect {
    return { x: left, y: top, left, top, right: left + width, bottom: top + height, width, height, toJSON: () => ({}) };
}

afterEach(async () => {
    document.documentElement.classList.remove(THEME_TRANSITION_CLASS);
    setPreferredTheme('light', 'porcelain');
    setPreferredTheme('dark', 'tokyo-night');
    setThemeMode('dark');
    themeController.destroy();
    if (component) await unmount(component);
    host?.remove();
    component = null;
    host = null;
    settingsView.set({ open: false, section: 'account' });
    vi.mocked(checkForUpdates).mockClear();
});

describe('SettingsWindow', () => {
    it('opens with the keyboard shortcut on the section shown last', async () => {
        setup();
        settingsView.set({ open: false, section: 'encryption' });
        window.dispatchEvent(new KeyboardEvent('keydown', { key: ',', metaKey: true }));
        await settle();

        expect(get(settingsView)).toEqual({ open: true, section: 'encryption' });
        expect(host?.querySelector('#settings-section-title')?.textContent).toBe('Encryption');
        expect(host?.querySelector('#settings-nav-encryption')?.getAttribute('aria-current')).toBe('page');
    });

    it('shows the light and the dark theme lists side by side', async () => {
        setup();
        openSettings('appearance');
        await settle();

        expect(host?.querySelector('[role="radiogroup"][aria-label="Light theme"]')).not.toBeNull();
        expect(host?.querySelector('[role="radiogroup"][aria-label="Dark theme"]')).not.toBeNull();

        host?.querySelector<HTMLButtonElement>('#appearance-theme-chalk')?.click();
        host?.querySelector<HTMLButtonElement>('#appearance-theme-nord')?.click();
        await settle();

        expect(host?.querySelector('#appearance-theme-chalk')?.getAttribute('aria-checked')).toBe('true');
        expect(host?.querySelector('#appearance-theme-nord')?.getAttribute('aria-checked')).toBe('true');
        expect(get(settingsView).open).toBe(true);
    });

    it('recovers a palette click intercepted by the theme transition layer', async () => {
        setup();
        openSettings('appearance');
        await settle();

        const card = host?.querySelector<HTMLElement>('.settings-card');
        const nord = host?.querySelector<HTMLButtonElement>('#appearance-theme-nord');
        if (!card || !nord) throw new Error('appearance controls did not render');
        vi.spyOn(card, 'getBoundingClientRect').mockReturnValue(rect(100, 100, 880, 620));
        vi.spyOn(nord, 'getBoundingClientRect').mockReturnValue(rect(500, 400, 180, 100));
        document.documentElement.classList.add(THEME_TRANSITION_CLASS);

        document.documentElement.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, clientX: 560, clientY: 450 }));
        await settle();

        expect(host?.querySelector('#appearance-theme-nord')?.getAttribute('aria-checked')).toBe('true');
        expect(get(settingsView).open).toBe(true);
    });

    it('checks for updates when the Updates section is shown, and hides it without an updater', async () => {
        setup();
        openSettings('updates');
        await settle();
        expect(checkForUpdates).toHaveBeenCalledOnce();
        if (component) await unmount(component);
        host?.remove();

        setup({ updaterAvailable: false });
        openSettings('account');
        await settle();
        expect(host?.querySelector('#settings-nav-updates')).toBeNull();
    });

    it('hands logging out and encryption to their own dialogs', async () => {
        const { onLogout, onEncryptionSettings } = setup();
        openSettings('account');
        await settle();

        host?.querySelector<HTMLButtonElement>('.settings-danger')?.click();
        await settle();
        expect(onLogout).toHaveBeenCalledOnce();
        expect(get(settingsView).open).toBe(false);
        expect(onEncryptionSettings).not.toHaveBeenCalled();
    });
});
