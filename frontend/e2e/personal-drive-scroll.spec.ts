import { expect, resolves, test, bootTDrive } from './wails-mock';

test.use({ viewport: { width: 390, height: 640 } });

test('phone channel picker scrolls to and selects a channel below the fold', async ({ page }) => {
    const channels = Array.from({ length: 12 }, (_, index) => ({
        id: String(index + 1),
        title: `Channel ${index + 1}`,
        created_at: 0,
        has_activity: false,
        recommended: false,
    }));
    const mock = await bootTDrive(page, {
        PreparePersonalDrive: resolves({ status: 'selection_required', active_channel_id: '' }),
        DiscoverPersonalDrives: resolves(channels),
        SelectPersonalDrive: resolves(null),
    }, { url: '/?mobile=android' });

    const picker = page.locator('.drive-setup');
    const lastChoice = picker.getByText('Channel 12', { exact: true });
    await expect(picker).toBeVisible();
    await expect(lastChoice).not.toBeInViewport();
    expect(await picker.evaluate((element) => element.scrollHeight > element.clientHeight)).toBe(true);

    await picker.hover({ position: { x: 50, y: 200 } });
    await page.mouse.wheel(0, 1600);
    await expect.poll(() => picker.evaluate((element) => element.scrollTop)).toBeGreaterThan(0);
    await expect(lastChoice).toBeInViewport();

    await lastChoice.click();
    await picker.getByRole('button', { name: 'Use this drive' }).click();
    await expect.poll(() => mock.calls('SelectPersonalDrive')).toMatchObject([
        { args: ['12'], state: 'fulfilled' },
    ]);
});
