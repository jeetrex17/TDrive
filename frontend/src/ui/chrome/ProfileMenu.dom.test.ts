import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, tick, unmount } from 'svelte';
import ProfileMenu from './ProfileMenu.svelte';
import { get } from 'svelte/store';
import {
    setPreferredTheme,
    setThemeMode,
    themeController,
} from '../theme/theme-controller';
import { closeSettings, settingsView } from '../settings/settings-store';
import { requestUpdatesPanel, updatesPanelRequest } from '../updates/update-store';
import { encryptionEntryVisible, profileLoaded, profileUser } from './profile-store';

let component: Record<string, unknown> | null = null;
let host: HTMLElement | null = null;

function setup(extra: { updaterAvailable?: boolean } = {}): void {
    host = document.createElement('div');
    document.body.appendChild(host);
    component = mount(ProfileMenu, {
        target: host,
        props: {
            onOpen: vi.fn(),
            onLogout: vi.fn(),
            ...extra,
        },
    });
    flushSync();
}

function click(selector: string): void {
    const element = host?.querySelector<HTMLElement>(selector);
    if (!element) throw new Error(`missing ${selector}`);
    element.click();
    flushSync();
}

afterEach(async () => {
    setPreferredTheme('light', 'porcelain');
    setPreferredTheme('dark', 'tokyo-night');
    setThemeMode('dark');
    themeController.destroy();
    if (component) await unmount(component);
    host?.remove();
    component = null;
    host = null;
    profileUser.set(null);
    profileLoaded.set(false);
    encryptionEntryVisible.set(false);
    updatesPanelRequest.set(0);
    closeSettings();
    settingsView.set({ open: false, section: 'account' });
});

describe('ProfileMenu appearance navigation', () => {
    it('omits local cache details from the account menu', () => {
        setup();
        click('#profile-trigger');

        expect(host?.querySelector('#profile-menu-storage')).toBeNull();
        expect(host?.querySelector('.photo-cache-panel')).toBeNull();
    });

    it('opens Settings from the menu and closes the menu', () => {
        setup();
        click('#profile-trigger');
        click('#profile-menu-settings');

        expect(host?.querySelector<HTMLElement>('#profile-menu')?.hidden).toBe(true);
        expect(get(settingsView).open).toBe(true);
    });

    it('sends update requests to the Updates section of Settings', async () => {
        setup();
        requestUpdatesPanel();
        flushSync();
        await tick();

        expect(get(settingsView)).toEqual({ open: true, section: 'updates' });
    });
});

describe('ProfileMenu without an updater', () => {
    it('ignores panel requests and omits the updates entry', async () => {
        setup({ updaterAvailable: false });
        requestUpdatesPanel();
        flushSync();
        await tick();

        expect(get(settingsView).open).toBe(false);

        click('#profile-trigger');
        await tick();
        await Promise.resolve();

        const menu = host?.querySelector<HTMLElement>('#profile-menu');
        expect(menu?.hidden).toBe(false);
        expect(menu?.getAttribute('role')).toBe('menu');
        expect(host?.querySelector('#profile-menu-updates')).toBeNull();
        expect(host?.querySelector('#profile-menu-settings')).toBe(document.activeElement);
    });
});
