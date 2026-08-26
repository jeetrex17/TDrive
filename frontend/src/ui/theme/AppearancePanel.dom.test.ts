import { get } from 'svelte/store';
import { afterEach, describe, expect, it } from 'vitest';
import { flushSync, mount, tick, unmount } from 'svelte';
import AppearancePanel from './AppearancePanel.svelte';
import { setPreferredTheme, setThemeMode, themeController, themeState } from './theme-controller';

let component: Record<string, unknown> | null = null;
let host: HTMLElement | null = null;

function setup(autofocus = false): void {
    host = document.createElement('div');
    document.body.appendChild(host);
    component = mount(AppearancePanel, { target: host, props: { autofocus } });
    flushSync();
}

function click(selector: string): void {
    const element = host?.querySelector<HTMLElement>(selector);
    if (!element) throw new Error(`missing ${selector}`);
    element.dispatchEvent(new MouseEvent('click', { bubbles: true, clientX: 80, clientY: 64 }));
    flushSync();
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
});

describe('AppearancePanel behavior', () => {
    it('shows only Light and Dark modes with the selected palette', () => {
        setup();

        expect(host?.textContent).not.toContain('System');
        expect(host?.querySelectorAll('[data-appearance-mode]')).toHaveLength(2);
        expect(host?.textContent).not.toContain('Automatic pair');
        expect(host?.querySelector('.appearance-toggle')).toBeNull();
        expect(host?.querySelector('.palette-section')).not.toBeNull();
    });

    it('supports arrow-key navigation through appearance modes', () => {
        setup();
        click('#appearance-mode-light');
        const light = host?.querySelector<HTMLElement>('#appearance-mode-light');
        if (!light) throw new Error('missing light mode');

        light.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowRight', bubbles: true }));
        flushSync();

        expect(get(themeState).preference.mode).toBe('dark');
        expect(host?.querySelector('#appearance-mode-dark')?.getAttribute('aria-checked')).toBe('true');
    });

    it('uses roving keyboard selection across the visible palette cards', () => {
        setup();
        click('#appearance-mode-dark');
        const tokyoNight = host?.querySelector<HTMLElement>('#appearance-theme-tokyo-night');
        if (!tokyoNight) throw new Error('missing Tokyo Night theme');

        tokyoNight.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true }));
        flushSync();

        expect(get(themeState).preference.darkThemeId).toBe('catppuccin-mocha');
        expect(host?.querySelector('#appearance-theme-catppuccin-mocha')?.getAttribute('aria-checked')).toBe('true');
    });

    it('supports keyboard boundaries without changing selection for unrelated keys', () => {
        setup();
        click('#appearance-mode-light');
        const light = host?.querySelector<HTMLElement>('#appearance-mode-light');
        if (!light) throw new Error('missing light mode');

        light.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', bubbles: true }));
        flushSync();
        expect(get(themeState).preference.mode).toBe('light');

        light.dispatchEvent(new KeyboardEvent('keydown', { key: 'End', bubbles: true }));
        flushSync();
        expect(get(themeState).preference.mode).toBe('dark');

        const tokyoNight = host?.querySelector<HTMLElement>('#appearance-theme-tokyo-night');
        if (!tokyoNight) throw new Error('missing Tokyo Night theme');
        tokyoNight.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', bubbles: true }));
        tokyoNight.dispatchEvent(new KeyboardEvent('keydown', { key: 'End', bubbles: true }));
        flushSync();
        expect(get(themeState).preference.darkThemeId).toBe('nord');

        host?.querySelector<HTMLElement>('#appearance-theme-nord')?.dispatchEvent(
            new KeyboardEvent('keydown', { key: 'Home', bubbles: true }),
        );
        flushSync();
        expect(get(themeState).preference.darkThemeId).toBe('tokyo-night');

        host?.querySelector<HTMLElement>('#appearance-theme-tokyo-night')?.dispatchEvent(
            new KeyboardEvent('keydown', { key: 'ArrowLeft', bubbles: true }),
        );
        flushSync();
        expect(get(themeState).preference.darkThemeId).toBe('nord');

        host?.querySelector<HTMLElement>('#appearance-theme-nord')?.dispatchEvent(
            new KeyboardEvent('keydown', { key: 'ArrowUp', bubbles: true }),
        );
        flushSync();
        expect(get(themeState).preference.darkThemeId).toBe('gruvbox-dark');
    });

    it('remembers palette choices while switching between Light and Dark', () => {
        setup();

        click('#appearance-mode-light');
        click('#appearance-theme-catppuccin-latte');
        click('#appearance-mode-dark');
        click('#appearance-theme-dracula');
        click('#appearance-mode-light');

        expect(get(themeState).preference).toMatchObject({
            mode: 'light',
            lightThemeId: 'catppuccin-latte',
            darkThemeId: 'dracula',
        });
        expect(host?.querySelector('#appearance-theme-catppuccin-latte')?.getAttribute('aria-checked')).toBe('true');
    });

    it('shows palette names without descriptions or auxiliary footer copy', () => {
        setup();
        click('#appearance-mode-dark');

        const tokyoNight = host?.querySelector('#appearance-theme-tokyo-night');
        const preview = tokyoNight?.querySelector('.theme-preview');
        const label = tokyoNight?.querySelector('.theme-label');
        expect(host?.textContent).toContain('Tokyo Night');
        expect(label?.previousElementSibling).toBe(preview);
        expect(host?.textContent).not.toContain('TDrive’s original midnight-blue glow.');
        expect(host?.textContent).not.toContain('Changes are previewed instantly and saved on this device.');
        expect(host?.querySelector('.theme-description')).toBeNull();
        expect(host?.querySelector('.appearance-back')).toBeNull();
    });

    it('autofocuses the selected mode when opened as a dialog', async () => {
        setup(true);
        await tick();
        await Promise.resolve();

        expect(host?.querySelector('#appearance-mode-dark')).toBe(document.activeElement);
    });
});
