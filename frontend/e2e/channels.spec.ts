import { expect, test } from '@playwright/test';
import { bootTDrive, resolves } from './wails-mock';

const source = { channel_id: 50, title: 'Field Recordings', username: 'fieldrec', connected: true, protected: false, available: true, account_id: 7, generation: 'source-a' };
const media = { msg_id: 71, date: 1_735_689_600, name: 'forest-dawn.mp4', size: 2048, mime_type: 'video/mp4', kind: 'video', caption: '', streamable: true, block_reason: '', telegram_url: 'https://t.me/fieldrec/71' };

test('desktop channels are a separate read-only source surface', async ({ page }) => {
    await bootTDrive(page, {
        ListConnectedChannelSources: resolves([source]),
        ListChannelSourceCandidates: resolves([source]),
        ListChannelMedia: resolves({ channel_id: 50, account_id: 7, generation: 'source-a', items: [media], next_offset_id: 0, has_more: false }),
    });
    await page.getByRole('button', { name: 'Channels' }).click();
    await expect(page.getByRole('heading', { name: 'Channels' })).toBeVisible();
    await expect(page.getByRole('button', { name: 'Channels', exact: true })).toHaveAttribute('aria-current', 'page');
    await expect(page.getByRole('button', { name: 'Personal', exact: true })).not.toHaveAttribute('aria-current', 'page');
    await expect(page.getByText('Read-only source')).toBeVisible();
    await expect(page.getByRole('button', { name: /Play forest-dawn/ })).toBeVisible();
    await expect(page.getByRole('button', { name: /Download/ })).toHaveCount(0);
});

test('mobile drive sheet reaches channels without switching the active drive', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await bootTDrive(page, {
        ListConnectedChannelSources: resolves([source]),
        ListChannelSourceCandidates: resolves([source]),
        ListChannelMedia: resolves({ channel_id: 50, account_id: 7, generation: 'source-a', items: [media], next_offset_id: 0, has_more: false }),
    }, { url: '/?mobile=ios' });
    await page.getByRole('button', { name: /Switch drive/ }).click();
    await page.getByRole('button', { name: 'Channels' }).click();
    await expect(page.getByRole('heading', { name: 'Channels' })).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Field Recordings' })).toBeVisible();
    await expect(page.locator('.drive-switcher-sheet')).toHaveAttribute('inert', '');
    await expect.poll(async () => (await page.locator('.drive-switcher-sheet').boundingBox())?.y ?? 0).toBeGreaterThanOrEqual(844);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
});
