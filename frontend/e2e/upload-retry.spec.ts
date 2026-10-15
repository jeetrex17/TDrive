import { bootTDrive, byFirstArg, expect, resolves, test } from './wails-mock';

for (const platform of ['desktop', 'android']) {
    test(`${platform}: retry only safe failed files after a partial batch`, async ({ page }) => {
        if (platform === 'android') {
            await page.setViewportSize({ width: 390, height: 844 });
            await page.addInitScript(() => history.replaceState(null, '', '/?mobile=android'));
        }
        const mock = await bootTDrive(page, {
            SelectFiles: resolves(['/tmp/accepted.txt', '/tmp/retry.txt', '/tmp/uncertain.txt']),
            PlanImport: resolves({ files: 3, folders: 0, archives: 0, limitExceeded: false }),
            RetryUploadToDriveFS: byFirstArg({ '/tmp/accepted.txt,/tmp/retry.txt,/tmp/uncertain.txt': resolves({
                result: { ok: false, error: { code: 'network_unavailable', message: 'Two files need attention' } },
                files: [{ name: 'accepted.txt', msg_id: 41, size: 3 }],
                failures: [
                    { index: 1, name: 'retry.txt', message: 'Connection unavailable before sending', code: 'temporary_failure', retryable: true },
                    { index: 2, name: 'uncertain.txt', message: 'Receipt was lost', code: 'outcome_unknown', retryable: false },
                ],
            }) }, resolves({ result: { ok: true }, files: [{ name: 'retry.txt', msg_id: 42 }], failures: [] })),
        });
        await expect(page.locator('#success-screen')).toBeVisible();
        if (platform === 'android') {
            await page.getByRole('group', { name: 'Files', exact: true }).getByRole('button', { name: 'Upload files', exact: true }).click();
        } else {
            await page.locator('#upload-btn').click();
            await page.locator('#upload-menu-files').click();
        }
        await page.getByRole('button', { name: 'Continue', exact: true }).click();
        // The report must survive dismissal of its transient toast.
        await page.getByRole('button', { name: 'Dismiss', exact: true }).click();
        if (platform === 'android') {
            await page.getByRole('navigation', { name: 'Primary' }).getByRole('button', { name: /^Transfers/ }).click();
            await page.getByRole('region', { name: 'Upload failures' }).getByRole('button', { name: 'Review failed files', exact: true }).click();
        } else {
            await page.locator('#notif-bell').click();
            await page.getByRole('dialog', { name: 'Notifications', exact: true }).getByRole('button', { name: 'View all activity', exact: true }).click();
            const activity = page.getByRole('dialog', { name: 'Activity', exact: true });
            await activity.getByRole('button', { name: 'View accepted.txt and 2 more', exact: true }).click();
            const files = activity.getByRole('region', { name: 'Files in upload', exact: true });
            await expect(files.locator('li').filter({ hasText: 'accepted.txt' })).toContainText('Completed');
            await expect(files.locator('li').filter({ hasText: 'retry.txt' })).toContainText('Failed');
            await expect(files.locator('li').filter({ hasText: 'uncertain.txt' })).toContainText('Receipt was lost');
            // The batch owns its review, so the list carries no second failure card.
            await expect(activity.getByRole('region', { name: 'Upload failures' })).toHaveCount(0);
            await activity.screenshot({ path: test.info().outputPath('activity-partial-batch-desktop.png') });
            await activity.getByRole('button', { name: 'Review failed files', exact: true }).click();
        }
        const dialog = page.getByRole('dialog', { name: 'Failed uploads' });
        await expect(dialog).toBeVisible();
        await expect(dialog.getByRole('list', { name: 'Failed files' })).toContainText('retry.txt');
        await expect(dialog.getByRole('list', { name: 'Failed files' })).toContainText('uncertain.txt');
        await expect(dialog.getByRole('list', { name: 'Failed files' })).not.toContainText('accepted.txt');
        await dialog.screenshot({ path: test.info().outputPath('failed-files.png') });
        await dialog.getByRole('button', { name: /^Retry failed files/ }).click();
        await expect.poll(() => mock.calls('RetryUploadToDriveFS')).toMatchObject([
            { args: [['/tmp/accepted.txt', '/tmp/retry.txt', '/tmp/uncertain.txt'], ['', '', ''], false, 1], state: 'fulfilled' },
            { args: [['/tmp/retry.txt'], [''], false, 1], state: 'fulfilled' },
        ]);
        expect(await mock.calls('UploadToDriveFS')).toHaveLength(0);
    });
}
