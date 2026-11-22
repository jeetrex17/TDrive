import { bootTDrive, byFirstArg, deferred, expect, resolves, test } from './wails-mock';

test('Activity keeps failed upload details through a restart until history is cleared', async ({ page }) => {
    const parent = 'd:archive';
    const reason = 'Connection unavailable before sending';
    const paths = ['/tmp/private-source/accepted.txt', '/tmp/private-source/retry.txt'];
    const mock = await bootTDrive(page, {
        MyUserID: resolves(42),
        GetFolderContents: byFirstArg({
            '': resolves({ folders: [{ id: parent, name: 'Archive', parent_id: '' }], files: [] }),
        }, resolves({ folders: [], files: [] })),
        SelectFiles: resolves(paths),
        PlanImport: resolves({ files: 2, folders: 0, archives: 0, limitExceeded: false }),
        RetryUploadToDriveFS: deferred('upload', {
            result: { ok: false, error: { code: 'network_unavailable', message: 'One file needs attention' } },
            files: [{ name: 'accepted.txt', msg_id: 41, size: 3 }],
            failures: [{ index: 1, name: 'retry.txt', message: reason, code: 'temporary_failure', retryable: true }],
        }),
    });

    await page.getByRole('row', { name: 'Folder: Archive', exact: true }).dblclick();
    await page.locator('#upload-btn').click();
    await page.locator('#upload-menu-files').click();
    await page.getByRole('button', { name: 'Continue', exact: true }).click();
    await expect.poll(() => mock.calls('RetryUploadToDriveFS')).toMatchObject([
        { args: [paths, [parent, parent], false, 1], state: 'pending' },
    ]);
    await mock.emit('upload_start', 1, 'retry.txt', 2048, parent);
    await mock.release('upload');

    const activity = page.getByRole('dialog', { name: 'Activity', exact: true });
    const batch = activity.getByRole('button', { name: 'View accepted.txt and 1 more', exact: true });
    const files = activity.getByRole('region', { name: 'Files in upload', exact: true });
    const failedFile = files.getByRole('listitem').filter({ hasText: 'retry.txt' });
    const retry = activity.getByRole('button', { name: 'Review failed files', exact: true });
    const transferList = activity.getByRole('list', { name: 'Transfers', exact: true });

    await page.locator('#notif-bell').click();
    await batch.click();
    await expect(failedFile).toContainText(reason);
    await expect(retry).toBeVisible();

    await page.reload();
    await page.locator('#notif-bell').click();
    await expect(transferList.getByRole('listitem')).toHaveCount(1);
    await expect(batch).toBeVisible();
    await batch.click();
    await expect(activity.getByRole('heading', { name: "Upload to Archive didn't finish" })).toBeVisible();
    await expect(activity.getByText('To retry, upload these files again.')).toBeVisible();
    await expect(failedFile).toContainText(reason);
    await expect(failedFile).toContainText('2 KB');
    await expect(files.getByText('accepted.txt', { exact: true })).toHaveCount(0);
    await expect(retry).toHaveCount(0);
    expect(await mock.calls('RetryUploadToDriveFS')).toEqual([]);
    expect(await mock.calls('UploadToDriveFS')).toEqual([]);

    await activity.getByRole('button', { name: 'Clear history', exact: true }).click();
    await activity.getByRole('button', { name: 'Clear', exact: true }).click();
    await expect(batch).toHaveCount(0);

    await page.reload();
    await page.locator('#notif-bell').click();
    await expect(activity.getByRole('heading', { name: 'No transfers yet', exact: true })).toBeVisible();
});
