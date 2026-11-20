import { bootTDrive, expect, resolves, test } from './wails-mock';

test('Android Back clears a Telegram source selection before leaving the source', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    const source = {
        peer_kind: 'channel', peer_id: 50, channel_id: 50, account_id: 7, generation: 'source-a',
        title: 'Field Recordings', username: 'fieldrec', connected: true, protected: false, available: true,
    };
    const file = {
        msg_id: 71, date: 1_735_689_600, name: 'forest-dawn.mp4', size: 2048, duration: 754, mime_type: 'video/mp4',
        kind: 'video', caption: '', streamable: true, block_reason: '', telegram_url: 'https://t.me/fieldrec/71',
        copy_identity: 'first',
    };
    await bootTDrive(page, {
        ListConnectedChannelSources: resolves([source]),
        ListChannelMedia: resolves({ ...source, items: [file], next_offset_id: 0, has_more: false }),
        Haptic: resolves(null),
    }, { url: '/?mobile=android' });

    await page.getByRole('button', { name: /Switch drive/ }).click();
    await page.locator('.drive-switcher-sheet').getByRole('button', { name: 'Channel Field Recordings', exact: true }).click();
    await page.getByRole('button', { name: 'Actions for forest-dawn.mp4' }).click();
    await page.getByRole('dialog', { name: file.name }).getByRole('button', { name: 'Select', exact: true }).click();

    const row = page.getByRole('button', { name: /^Play forest-dawn\.mp4/ });
    const save = page.getByRole('button', { name: 'Save 1 file to My Drive' });
    await expect(save).toBeVisible();
    const focusedOnEntry = await row.evaluate((element) => document.activeElement === element);

    await page.getByRole('button', { name: /^Transfers/ }).click();
    expect(await page.evaluate(() => window.__tdriveHandleBack?.())).toBe(true);
    await expect(save).toBeVisible();

    expect(await page.evaluate(() => window.__tdriveHandleBack?.())).toBe(true);
    const title = page.getByRole('heading', { name: source.title });
    await expect(title).toBeVisible();
    await expect(save).toBeHidden();
    await expect(row).toBeFocused();
    expect(focusedOnEntry).toBe(true);

    expect(await page.evaluate(() => window.__tdriveHandleBack?.())).toBe(true);
    await expect(title).toBeHidden();
});
