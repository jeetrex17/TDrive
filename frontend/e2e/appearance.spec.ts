import { bootTDrive, expect, test } from './wails-mock';
import type { Page } from '@playwright/test';

type Platform = 'desktop' | 'android' | 'ios';

async function usePlatform(page: Page, platform: Platform): Promise<void> {
    if (platform === 'desktop') return;
    await page.setViewportSize({ width: 390, height: 844 });
    await page.addInitScript((mobile) => history.replaceState(null, '', `/?mobile=${mobile}`), platform);
}

async function openAppearance(page: Page, platform: Platform): Promise<void> {
    if (platform === 'desktop') {
        await page.locator('#profile-trigger').click();
        await page.getByRole('menuitem', { name: 'Appearance' }).click();
        return;
    }

    await page.getByRole('navigation', { name: 'Primary' })
        .getByRole('button', { name: 'Account', exact: true }).click();
    await page.getByRole('button', { name: /Appearance/ }).click();
}

for (const platform of ['desktop', 'android', 'ios'] as const) {
    test(`appearance offers only Light and Dark on ${platform}`, async ({ page }) => {
        await usePlatform(page, platform);
        await bootTDrive(page);
        await openAppearance(page, platform);

        const modes = page.getByRole('radiogroup', { name: 'Appearance mode' });
        await expect(modes.getByRole('radio')).toHaveCount(2);
        await expect(modes.getByRole('radio', { name: 'System' })).toHaveCount(0);

        await modes.getByRole('radio', { name: 'Light' }).click();
        await expect(page.locator('html')).toHaveAttribute('data-theme-appearance', 'light');
        await modes.getByRole('radio', { name: 'Dark' }).click();
        await expect(page.locator('html')).toHaveAttribute('data-theme-appearance', 'dark');
    });
}

test('a saved System preference becomes fixed at the current appearance', async ({ page }) => {
    await page.emulateMedia({ colorScheme: 'light' });
    await page.addInitScript(() => {
        localStorage.setItem('tdrive.appearance.v1', JSON.stringify({
            mode: 'system',
            lightThemeId: 'porcelain',
            darkThemeId: 'nord',
        }));
    });
    await bootTDrive(page);

    await expect(page.locator('html')).toHaveAttribute('data-theme', 'porcelain');
    await expect.poll(() => page.evaluate(() => JSON.parse(localStorage.getItem('tdrive.appearance.v1') ?? '{}').mode))
        .toBe('light');

    await page.emulateMedia({ colorScheme: 'dark' });
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'porcelain');
});
