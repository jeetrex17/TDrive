import { get } from 'svelte/store';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import AppearancePanel from './AppearancePanel.svelte';
import { setPreferredTheme, setThemeMode, themeController, themeState } from './theme-controller';

let component: Record<string, unknown> | null = null;
let host: HTMLElement | null = null;

function setup(onBack = vi.fn()): typeof onBack {
    host = document.createElement('div');
    document.body.appendChild(host);
    component = mount(AppearancePanel, { target: host, props: { onBack } });
    flushSync();
    return onBack;
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
    it('lets System users configure the day and night palettes independently', () => {
        setup();

        click('[role="tab"][aria-selected="false"]');
        expect(host?.textContent).toContain('Tokyo Night');
        expect(host?.textContent).toContain('Dracula');

        click('#appearance-theme-dracula');
        expect(get(themeState).preference.darkThemeId).toBe('dracula');
        expect(get(themeState).preference.lightThemeId).toBe('tdrive-light');
    });

    it('supports arrow-key navigation through appearance modes', () => {
        setup();
        const automatic = host?.querySelector<HTMLElement>('#appearance-mode-system');
        if (!automatic) throw new Error('missing automatic mode');

        automatic.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowRight', bubbles: true }));
        flushSync();

        expect(get(themeState).preference.mode).toBe('light');
        expect(host?.querySelector('#appearance-mode-light')?.getAttribute('aria-checked')).toBe('true');
    });

    it('supports keyboard navigation across the automatic day and night pair', () => {
        setup();
        const dayTab = host?.querySelector<HTMLElement>('#appearance-system-light');
        if (!dayTab) throw new Error('missing day palette tab');

        dayTab.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowRight', bubbles: true }));
        flushSync();

        expect(host?.querySelector('#appearance-system-dark')?.getAttribute('aria-selected')).toBe('true');
        expect(host?.textContent).toContain('Tokyo Night');

        host?.querySelector<HTMLElement>('#appearance-system-dark')?.dispatchEvent(
            new KeyboardEvent('keydown', { key: 'ArrowLeft', bubbles: true }),
        );
        flushSync();
        expect(host?.querySelector('#appearance-system-light')?.getAttribute('aria-selected')).toBe('true');
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

    it('returns focus ownership to the account menu through its back action', () => {
        const onBack = setup();

        click('.appearance-back');

        expect(onBack).toHaveBeenCalledOnce();
    });
});
