import { bootTDrive, expect, test } from './wails-mock';

test('a row in the bell panel opens its Activity detail and preserves the failure', async ({ page }) => {
    const mock = await bootTDrive(page);
    await expect(page.locator('#success-screen')).toBeVisible();
    await mock.emit('upload_start', 41, 'broken.bin', 1024, '');
    await mock.emit('upload_error', 41, 'broken.bin', 'upload part: rpc error code 400: FILE_REFERENCE_EXPIRED');

    const bell = page.locator('#notif-bell');
    await bell.focus();
    await bell.hover();
    const preview = page.getByRole('dialog', { name: 'Notifications', exact: true });
    await expect(preview).toBeVisible();
    await expect(bell).toBeFocused();
    await preview.getByRole('button', { name: 'broken.bin', exact: true }).click();

    const activity = page.getByRole('dialog', { name: 'Activity', exact: true });
    await expect(activity).toBeVisible();
    await expect(preview).toBeHidden();
    await expect(activity.getByRole('searchbox', { name: 'Search activity' })).toBeFocused();
    const details = activity.getByRole('region', { name: 'Details for broken.bin', exact: true });
    await expect(details).toContainText('Failed');
    await expect(details).toContainText(/fresh reference/);
    await activity.screenshot({ path: test.info().outputPath('activity-failure-desktop.png') });
    await page.keyboard.press('Escape');
    await expect(activity).toBeHidden();
    await expect(bell).toBeFocused();
    await expect(page.locator('#file-list')).toBeVisible();
});

test('Activity shows the full restored notice and the bell stays a quick panel', async ({ page }) => {
    const body = 'The destination drive is temporarily unavailable. Your files remain on this device. Reconnect to the drive, then review the failed files before retrying. No successful files need to be uploaded again.';
    await page.addInitScript((message) => {
        localStorage.setItem('tdrive.transfers.v1', JSON.stringify({
            version: 1,
            savedAt: Date.now(),
            events: [{ kind: 'event', id: 'evt:retained-notice', level: 'warning', title: 'Destination unavailable', body: message, ts: Date.now() }],
        }));
    }, body);
    await bootTDrive(page);
    const bell = page.locator('#notif-bell');
    await expect(bell).toBeVisible();
    await bell.focus();
    await page.keyboard.press('Enter');
    const preview = page.getByRole('dialog', { name: 'Notifications', exact: true });
    await expect(preview).toBeVisible();
    await preview.getByRole('button', { name: 'View all activity', exact: true }).click();
    const activity = page.getByRole('dialog', { name: 'Activity', exact: true });
    await expect(activity).toBeVisible();
    await activity.getByRole('tab', { name: 'Notifications', exact: true }).click();
    await activity.getByRole('button', { name: 'View Destination unavailable', exact: true }).click();
    const details = activity.getByRole('region', { name: 'Details for Destination unavailable', exact: true });
    await expect(details).toContainText(body);
    expect(await details.evaluate((element) => element.scrollWidth <= element.clientWidth + 1)).toBe(true);
    await activity.screenshot({ path: test.info().outputPath('activity-notice-desktop.png') });
    await activity.getByRole('button', { name: 'Close activity', exact: true }).click();
    await expect(bell).toBeFocused();
    await page.keyboard.press('Space');
    await expect(preview).toBeVisible();
    await expect(activity).toBeHidden();
    await page.keyboard.press('Escape');
    await expect(preview).toBeHidden();
    await expect(bell).toBeFocused();
});
