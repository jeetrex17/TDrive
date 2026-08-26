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
    setThemeMode('system');
    themeController.destroy();
    if (component) await unmount(component);
    host?.remove();
    component = null;
    host = null;
});

describe('AppearancePanel behavior', () => {
    it('keeps System mode free of nested palette controls', () => {
        setup();

        expect(host?.textContent).toContain('System');
        expect(host?.textContent).not.toContain('Automatic pair');
        expect(host?.querySelector('.appearance-toggle')).toBeNull();
        expect(host?.querySelector('.palette-section')).toBeNull();
    });

    it('supports arrow-key navigation through appearance modes', () => {
        setup();
        const system = host?.querySelector<HTMLElement>('#appearance-mode-system');
        if (!system) throw new Error('missing system mode');

        system.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowRight', bubbles: true }));
        flushSync();

        expect(get(themeState).preference.mode).toBe('light');
        expect(host?.querySelector('#appearance-mode-light')?.getAttribute('aria-checked')).toBe('true');
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
        const system = host?.querySelector<HTMLElement>('#appearance-mode-system');
        if (!system) throw new Error('missing system mode');

        system.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', bubbles: true }));
        flushSync();
        expect(get(themeState).preference.mode).toBe('system');

        system.dispatchEvent(new KeyboardEvent('keydown', { key: 'End', bubbles: true }));
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

    it('remembers Light and Dark choices when returning to System', () => {
        setup();

        click('#appearance-mode-light');
        click('#appearance-theme-catppuccin-latte');
        click('#appearance-mode-dark');
        click('#appearance-theme-dracula');
        click('#appearance-mode-system');

        expect(get(themeState).preference).toMatchObject({
            mode: 'system',
            lightThemeId: 'catppuccin-latte',
            darkThemeId: 'dracula',
        });
        expect(host?.querySelector('.palette-section')).toBeNull();
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

        expect(host?.querySelector('#appearance-mode-system')).toBe(document.activeElement);
    });
});
