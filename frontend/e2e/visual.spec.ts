/**
 * Screenshot guard for the main surfaces. The layout spec checks the geometry
 * rules exactly; these catch what numbers do not name: a colour, a weight, a
 * missing hairline, a control that changed shape. Run in WebKit on macOS (the
 * webkit-media project), where the baselines were recorded, because text
 * rendering differs between platforms. After an intended visual change,
 * refresh them with:
 *
 *   npx playwright test e2e/visual.spec.ts --project=webkit-media --update-snapshots
 */

import { bootTDrive, byFirstArg, expect, resolves, test } from './wails-mock';
import { FIRST_PHOTO, SECOND_PHOTO, galleryPlans, routeRenditions } from './gallery-fixtures';
import type { Page } from '@playwright/test';

const NOW = new Date('2026-01-15T10:00:00Z');
const T = Math.floor(NOW.getTime() / 1000);
const file = (msg_id: number, name: string, size: number, days: number) => ({
    name, size, msg_id, parent_id: '', upload_time: T - days * 86_400, uploader_id: 7, encrypted: false, plaintext_size: 0,
});
const plans = {
    ListChannels: resolves([
        { id: 1, title: 'My Drive', kind: 'personal', is_active: true, invite_link: '' },
        { id: 2, title: 'Team assets', kind: 'shared', is_active: false, invite_link: 'https://t.me/x' },
    ]),
    GetFolderContents: byFirstArg({
        '': resolves({
            folders: [
                { id: 'a', name: 'Documents', parent_id: '' },
                { id: 'b', name: 'Photos 2025', parent_id: '' },
                { id: 'c', name: 'Projects', parent_id: '' },
            ],
            files: [
                file(101, 'Quarterly report.pdf', 2_400_000, 1),
                file(102, 'Budget 2026.xlsx', 48_000, 3),
                file(103, 'Lisbon trip.mp4', 812_000_000, 6),
                file(104, 'IMG_2041.jpg', 4_100_000, 9),
                file(105, 'notes.md', 3_200, 12),
                file(106, 'archive-backup.zip', 2_300_000_000, 44),
            ],
        }),
    }, resolves({ folders: [], files: [] })),
    ListTrash: resolves([]),
};

async function prepare(page: Page, darkThemeId = 'quiet-relay') {
    await page.clock.setFixedTime(NOW);
    await page.addInitScript((theme) => {
        localStorage.setItem('tdrive.appearance.v1', JSON.stringify({ mode: 'dark', lightThemeId: 'daybreak', darkThemeId: theme }));
    }, darkThemeId);
}

async function settle(page: Page) {
    await page.waitForFunction(() => document.getAnimations().every((animation) => animation.playState !== 'running'));
    await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
}

for (const theme of ['quiet-relay', 'graphite']) {
    test(`desktop files in ${theme}`, async ({ page }) => {
        await prepare(page, theme);
        await bootTDrive(page, plans);
        await expect(page.getByRole('row', { name: /Quarterly report/ })).toBeVisible();
        await settle(page);
        await expect(page).toHaveScreenshot(`desktop-files-${theme}.png`);
    });
}

test('desktop dialog', async ({ page }) => {
    await prepare(page);
    await bootTDrive(page, plans);
    await expect(page.getByRole('row', { name: /Quarterly report/ })).toBeVisible();
    await page.locator('#open-new-drive').click();
    await expect(page.getByRole('dialog')).toBeVisible();
    await settle(page);
    await expect(page).toHaveScreenshot('desktop-new-drive.png');
});

test('desktop media grid', async ({ page }) => {
    await prepare(page);
    await routeRenditions(page);
    await bootTDrive(page, galleryPlans([FIRST_PHOTO, SECOND_PHOTO]));
    await page.getByRole('button', { name: 'Media' }).click();
    await expect(page.getByRole('button', { name: FIRST_PHOTO.name }).locator('img')).toHaveAttribute('src', /^blob:/);
    await settle(page);
    await expect(page).toHaveScreenshot('desktop-media.png');
});

test('desktop sign-in', async ({ page }) => {
    await prepare(page);
    await bootTDrive(page, { CheckLoginStatus: resolves(false), LoginPhoneNumber: resolves(null) });
    await expect(page.locator('#telegram-country')).toBeVisible();
    await settle(page);
    await expect(page).toHaveScreenshot('desktop-sign-in.png');
});

test.describe('phone', () => {
    test.use({ viewport: { width: 390, height: 844 } });

    for (const tab of ['Files', 'Account'] as const) {
        test(`phone ${tab.toLowerCase()}`, async ({ page }) => {
            await prepare(page);
            await page.addInitScript(() => history.replaceState(null, '', '/?mobile=ios'));
            await bootTDrive(page, plans);
            await expect(page.locator('#success-screen.mobile-shell')).toBeVisible();
            await expect(page.locator('#file-list .drive-row').first()).toBeVisible();
            if (tab !== 'Files') {
                await page.getByRole('navigation', { name: 'Primary' }).getByRole('button', { name: new RegExp(`^${tab}`) }).click();
            }
            await settle(page);
            await expect(page).toHaveScreenshot(`phone-${tab.toLowerCase()}.png`);
        });
    }
});
