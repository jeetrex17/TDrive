import { bootTDrive, byFirstArg, expect, resolves, test } from './wails-mock';

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
        MergeImportPathsForDrive: resolves({ ok: true }),
        ImportPathsForDrive: resolves({ ok: true }),
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
    await expect.poll(() => mock.calls('MergeImportPathsForDrive')).toEqual([
        { method: 'MergeImportPathsForDrive', args: [paths, parent, false, false, 1, 'keep-both'], state: 'fulfilled' },
    ]);

    await page.locator('#upload-btn').click();
    await page.locator('#upload-menu-folder').click();
    await expect(merge).not.toBeChecked();
    await expect(dialog.getByRole('radiogroup', { name: 'Files with the same name' })).toBeHidden();
    await dialog.getByRole('button', { name: 'Import', exact: true }).click();
    await expect.poll(() => mock.calls('ImportPathsForDrive')).toEqual([
        { method: 'ImportPathsForDrive', args: [paths, parent, false, false, 1], state: 'fulfilled' },
    ]);
    expect(await mock.calls('MergeImportPathsForDrive')).toHaveLength(1);
});
