import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, tick, unmount } from 'svelte';
import ProfileMenu from './ProfileMenu.svelte';
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

afterEach(async () => {
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
        expect(host?.textContent).toContain('System');
        expect(host?.textContent).not.toContain('Automatic pair');
        expect(host?.querySelector('.appearance-back')).toBeNull();
        const selectedMode = host?.querySelector('[data-appearance-mode="system"]');
        expect(selectedMode).toBe(document.activeElement);

        menu?.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }));
        flushSync();
        await tick();
        await Promise.resolve();

        expect(menu?.hidden).toBe(false);
        expect(menu?.getAttribute('role')).toBe('menu');
        expect(host?.querySelector('#profile-menu-appearance')).toBe(document.activeElement);
    });
});
