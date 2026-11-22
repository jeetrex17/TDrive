/**
 * The desktop Settings window: one place for the account, appearance, backup,
 * encryption and updates, opened from the account menu or with ⌘, / Ctrl+,.
 * It is a modal, so the keyboard contract matters as much as what it shows.
 */

import { bootTDrive, expect, test } from './wails-mock';
import { openSettingsSection } from './settings-helpers';

test('the shortcut opens Settings, arrows move between sections, Escape returns focus', async ({ page }) => {
    await bootTDrive(page);
    const search = page.locator('#search-input');
    await search.focus();

    await page.keyboard.press('ControlOrMeta+Comma');
    const settings = page.getByRole('dialog', { name: 'Settings' });
    await expect(settings).toBeVisible();
    const account = settings.getByRole('button', { name: 'Account', exact: true });
    await expect(account).toBeFocused();
    await expect(account).toHaveAttribute('aria-current', 'page');

    await page.keyboard.press('ArrowDown');
    const appearance = settings.getByRole('button', { name: 'Appearance', exact: true });
    await expect(appearance).toBeFocused();
    await expect(settings.getByRole('heading', { name: 'Appearance', level: 3 })).toBeVisible();
    await expect(settings.getByRole('radiogroup', { name: 'Light theme' })).toBeVisible();
    await expect(settings.getByRole('radiogroup', { name: 'Dark theme' })).toBeVisible();

    // Back where the reader was before the shortcut.
    await page.keyboard.press('Escape');
    await expect(settings).toBeHidden();
    await expect(search).toBeFocused();
});

test('closing Settings opened from the account menu returns to the account button', async ({ page }) => {
    await bootTDrive(page);
    const settings = await openSettingsSection(page, 'Encryption');
    await page.keyboard.press('Escape');
    await expect(settings).toBeHidden();
    await expect(page.locator('#profile-trigger')).toBeFocused();
});

test('the account menu keeps only Settings, updates and log out', async ({ page }) => {
    await bootTDrive(page);
    await page.locator('#profile-trigger').click();
    const menu = page.getByRole('menu');
    await expect(menu.getByRole('menuitem')).toHaveText([/Settings…/, /Check for updates/, /Log out/]);
});

test('choosing a theme for the other mode keeps the current one on screen', async ({ page }) => {
    await bootTDrive(page);
    const settings = await openSettingsSection(page, 'Appearance');
    const before = await page.locator('html').getAttribute('data-theme');

    await settings.getByRole('radiogroup', { name: 'Light theme' }).getByRole('radio', { name: 'Chalk' }).click();
    await expect(settings.getByRole('radio', { name: 'Chalk' })).toHaveAttribute('aria-checked', 'true');
    await expect(page.locator('html')).toHaveAttribute('data-theme', before ?? '');

    await settings.getByRole('radio', { name: 'Light', exact: true }).click();
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'chalk');
});
