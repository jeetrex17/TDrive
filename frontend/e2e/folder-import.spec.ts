import { bootTDrive, byFirstArg, deferred, expect, resolves, test } from './wails-mock';

test('folder import uses the selected merge policy and defaults to a separate copy on the next import', async ({ page }) => {
    const paths = ['/tmp/Photos'];
    const parent = 'd:archive';
    const mock = await bootTDrive(page, {
        GetFolderContents: byFirstArg({
            '': resolves({ folders: [{ id: parent, name: 'Archive', parent_id: '' }], files: [] }),
            [parent]: resolves({ folders: [{ id: 'd:photos', name: 'Photos', parent_id: parent }], files: [] }),
        }, resolves({ folders: [], files: [] })),
        SelectFolder: resolves(paths[0]),
        PlanImport: resolves({ files: 2, folders: 1, bytes: 48, archives: 0, limitExceeded: false }),
        ImportPathsWithRetryForDrive: resolves({ result: { ok: true }, retryFiles: [] }),
    });
    await page.getByRole('row', { name: 'Folder: Archive', exact: true }).dblclick();

    const dialog = page.getByRole('dialog', { name: 'Import', exact: true });
    const merge = dialog.getByRole('checkbox', { name: /^Merge matching folders/ });
    await page.locator('#upload-btn').click();
    await page.locator('#upload-menu-folder').click();
    await expect(merge).not.toBeChecked();
    await merge.check();
    await expect(dialog.getByRole('radio', { name: 'Skip files with the same name' })).toBeChecked();
    await dialog.getByRole('radio', { name: 'Keep both files' }).check();
    await dialog.getByRole('button', { name: 'Import', exact: true }).click();
    await expect.poll(() => mock.calls('ImportPathsWithRetryForDrive')).toMatchObject([
        { args: [paths, parent, false, false, 1, 'keep-both'], state: 'fulfilled' },
    ]);

    await page.locator('#upload-btn').click();
    await page.locator('#upload-menu-folder').click();
    await expect(merge).not.toBeChecked();
    await expect(dialog.getByRole('radiogroup', { name: 'Files with the same name' })).toBeHidden();
    await dialog.getByRole('button', { name: 'Import', exact: true }).click();
    await expect.poll(() => mock.calls('ImportPathsWithRetryForDrive')).toMatchObject([
        { args: [paths, parent, false, false, 1, 'keep-both'], state: 'fulfilled' },
        { args: [paths, parent, false, false, 1, ''], state: 'fulfilled' },
    ]);
});

test('Activity explains an interrupted folder import and retries only the eligible failed file', async ({ page }) => {
    const root = '/tmp/Photos';
    const parent = 'd:archive';
    const interruption = 'Connection unavailable before the import finished';
    const unattempted = 'About 97 files were never attempted';
    const manualRecovery = 'Let the original destination sync, then upload any files that are still missing.';
    const retryFile = {
        path: `${root}/2024/retry.jpg`,
        parentId: 'd:photos-2024',
        relativePath: 'Photos/2024/retry.jpg',
        size: 32,
        modifiedAt: '2026-11-22T06:00:00Z',
        message: 'Connection unavailable before sending',
        code: 'temporary_failure',
    };
    const mock = await bootTDrive(page, {
        GetFolderContents: byFirstArg({
            '': resolves({ folders: [{ id: parent, name: 'Archive', parent_id: '' }], files: [] }),
        }, resolves({ folders: [], files: [] })),
        SelectFolder: resolves(root),
        PlanImport: resolves({ files: 100, folders: 3, bytes: 3200, archives: 0, limitExceeded: false }),
        ImportPathsWithRetryForDrive: deferred('folder-import', {
            result: { ok: false, error: { code: 'network_unavailable', message: interruption } }, retryFiles: [retryFile],
        }),
        RetryImportedFilesForDrive: resolves({
            result: { ok: true }, files: [{ name: 'retry.jpg', msg_id: 42, size: 32 }], failures: [],
        }),
    });

    await page.getByRole('row', { name: 'Folder: Archive', exact: true }).dblclick();
    await page.locator('#upload-btn').click();
    await page.locator('#upload-menu-folder').click();
    await page.getByRole('dialog', { name: 'Import', exact: true }).getByRole('button', { name: 'Import', exact: true }).click();
    await expect.poll(() => mock.calls('ImportPathsWithRetryForDrive')).toMatchObject([
        { args: [[root], parent, false, false, 1, ''], state: 'pending' },
    ]);
    await mock.emit('import_start');
    await mock.emit('import_uploading', { files: 100, folders: 3 });
    await mock.emit('import_upload_progress', {
        files: 3, done: 1, failed: 2, settled: [
            { id: 0, name: 'accepted.jpg', path: 'Photos/2023/accepted.jpg', size: 32, status: 'done' },
            { id: 1, name: 'retry.jpg', path: retryFile.relativePath, size: 32, status: 'failed', message: retryFile.message },
            { id: 2, name: 'uncertain.jpg', path: 'Photos/2024/uncertain.jpg', size: 32, status: 'failed', message: 'Receipt was lost' },
        ],
    });
    await mock.emit('import_complete', {
        status: 'failed', error: interruption, uploaded: 1, failed: 2, scheduled: 3, folders: 3, unattempted: 97,
    });
    await mock.release('folder-import');

    const activity = page.getByRole('dialog', { name: 'Activity', exact: true });
    const batch = activity.getByRole('button', { name: 'View Photos', exact: true });
    const review = activity.getByRole('button', { name: 'Review failed files', exact: true });
    await page.locator('#notif-bell').click();
    await batch.click();
    const files = activity.getByRole('region', { name: 'Files in upload', exact: true });
    await expect(files.getByRole('listitem').filter({ hasText: 'accepted.jpg' })).toContainText('Completed');
    await expect(files.getByRole('listitem').filter({ hasText: 'uncertain.jpg' })).toContainText('Receipt was lost');
    await expect(activity.getByText(unattempted, { exact: false })).toBeVisible();
    await expect(activity.getByText(manualRecovery, { exact: false })).toBeVisible();
    await expect(review).toBeVisible();
    await review.click();

    const dialog = page.getByRole('dialog', { name: 'Failed uploads', exact: true });
    const retry = dialog.getByRole('button', { name: /^Retry failed files/ });
    await expect(dialog.getByRole('list', { name: 'Failed files' })).toContainText('Photos/2024/retry.jpg');
    await expect(dialog.getByText(unattempted, { exact: false })).toBeVisible();
    await expect(dialog.getByText(manualRecovery, { exact: false })).toBeVisible();
    await expect(retry).toHaveText('Retry failed files (1)');
    await retry.click();
    await expect.poll(() => mock.calls('RetryImportedFilesForDrive')).toMatchObject([
        { args: [[retryFile], false, 1], state: 'fulfilled' },
    ]);

    await expect(dialog).toBeHidden();
    await review.click();
    await expect(retry).toBeDisabled();
    await expect(dialog.getByText('Retry started', { exact: true })).toBeVisible();
    expect(await mock.calls('ImportPathsWithRetryForDrive')).toHaveLength(1);
    expect(await mock.calls('RetryImportedFilesForDrive')).toHaveLength(1);
});
