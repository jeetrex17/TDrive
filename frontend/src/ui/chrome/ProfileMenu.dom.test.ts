import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, tick, unmount } from 'svelte';
import ProfileMenu from './ProfileMenu.svelte';
import {
    setPreferredTheme,
    setThemeMode,
    THEME_TRANSITION_CLASS,
    themeController,
} from '../theme/theme-controller';
import { encryptionEntryVisible, profileLoaded, profileUser } from './profile-store';

let component: Record<string, unknown> | null = null;
let host: HTMLElement | null = null;

function setup(): void {
    host = document.createElement('div');
    document.body.appendChild(host);
    component = mount(ProfileMenu, {
        target: host,
        props: {
            onOpen: vi.fn(),
            onEncryptionSettings: vi.fn(),
            onLogout: vi.fn(),
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

function rect(left: number, top: number, width: number, height: number): DOMRect {
    return {
        x: left,
        y: top,
        left,
        top,
        right: left + width,
        bottom: top + height,
        width,
        height,
        toJSON: () => ({}),
    };
}

afterEach(async () => {
    setPreferredTheme('light', 'tdrive-light');
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
});

describe('ProfileMenu appearance navigation', () => {
    it('uses Escape to return to the account menu before closing the popover', async () => {
        setup();
        click('#profile-trigger');
        click('#profile-menu-appearance');
        await tick();
        await Promise.resolve();

        const menu = host?.querySelector<HTMLElement>('#profile-menu');
        expect(menu?.getAttribute('role')).toBe('dialog');
        expect(menu?.getAttribute('aria-labelledby')).toBe('appearance-title');
        expect(host?.textContent).not.toContain('System');
        expect(host?.textContent).not.toContain('Automatic pair');
        expect(host?.querySelector('.appearance-back')).toBeNull();
        const selectedMode = host?.querySelector('[data-appearance-mode="dark"]');
        expect(selectedMode).toBe(document.activeElement);

        menu?.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
        flushSync();
        await tick();
        await Promise.resolve();

        expect(menu?.hidden).toBe(false);
        expect(menu?.getAttribute('role')).toBe('menu');
        expect(host?.querySelector('#profile-menu-appearance')).toBe(document.activeElement);
    });

    it('stays open while previewing multiple palettes', async () => {
        setup();
        click('#profile-trigger');
        click('#profile-menu-appearance');
        await tick();

        click('#appearance-theme-dracula');
        await tick();
        click('#appearance-theme-nord');
        await tick();

        const menu = host?.querySelector<HTMLElement>('#profile-menu');
        expect(menu?.hidden).toBe(false);
        expect(menu?.getAttribute('role')).toBe('dialog');
        expect(host?.querySelector('#appearance-theme-nord')?.getAttribute('aria-checked')).toBe('true');
        expect(host?.textContent).toContain('Nord is active.');
    });

    it('recovers a rapid palette click intercepted by the root transition layer', async () => {
        setup();
        click('#profile-trigger');
        click('#profile-menu-appearance');
        await tick();
        await Promise.resolve();

        const menu = host?.querySelector<HTMLElement>('#profile-menu');
        const nord = host?.querySelector<HTMLButtonElement>('#appearance-theme-nord');
        if (!menu || !nord) throw new Error('appearance controls did not render');
        vi.spyOn(menu, 'getBoundingClientRect').mockReturnValue(rect(100, 100, 390, 680));
        vi.spyOn(nord, 'getBoundingClientRect').mockReturnValue(rect(290, 360, 180, 100));
        document.documentElement.classList.add(THEME_TRANSITION_CLASS);

        document.documentElement.dispatchEvent(new MouseEvent('click', {
            bubbles: true,
            cancelable: true,
            clientX: 340,
            clientY: 410,
        }));
        flushSync();
        await tick();

        expect(menu.hidden).toBe(false);
        expect(menu.getAttribute('role')).toBe('dialog');
        expect(host?.querySelector('#appearance-theme-nord')?.getAttribute('aria-checked')).toBe('true');
        expect(document.activeElement).toBe(host?.querySelector('#appearance-theme-nord'));
    });
});
