import type { Locator, Page } from '@playwright/test';

/**
 * Opens the desktop Settings window on a section, the way a reader does. When
 * Settings is already open (a dialog it raised has just closed over it), it
 * moves to the section instead: the account menu sits behind the window.
 */
export async function openSettingsSection(page: Page, section: string): Promise<Locator> {
    const settings = page.getByRole('dialog', { name: 'Settings' });
    if (!(await settings.isVisible())) {
        await page.locator('#profile-trigger').click();
        await page.getByRole('menuitem', { name: 'Settings…' }).click();
    }
    await settings.getByRole('button', { name: section, exact: true }).click();
    return settings;
}
