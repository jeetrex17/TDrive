import { bootTDrive, expect, resolves, test } from './wails-mock';

test('the phone copies selected Shared Drive files from the selection bar', async ({ page }) => {
    await page.setViewportSize({ width: 320, height: 700 });
    const first = { msg_id: 42, name: 'first.pdf', size: 321, revision: 3, uploader_id: 8 };
    const second = { ...first, msg_id: 43, name: 'second.pdf' };
    const encrypted = { ...first, msg_id: 44, name: 'vault.pdf', encrypted: true };
    const names = [first.name, second.name];
    const mock = await bootTDrive(page, {
        ListChannels: resolves([
            { id: 1, title: 'Personal', kind: 'personal', is_active: false, invite_link: '' },
            { id: 2, title: 'Shared files', kind: 'shared', is_active: true, invite_link: '' },
        ]),
        GetFolderContents: resolves({ folders: [], files: [first, second, encrypted] }),
        ListSourceCopyFolders: resolves({ drive_id: 1, folders: [] }),
        PreviewSourceCopyNames: resolves(names),
        ResolveUsernames: resolves({ '8': 'Uploader' }),
    }, { url: '/?mobile=ios' });

    await page.getByRole('button', { name: /Sort and more/ }).click();
    await page.getByRole('menuitem', { name: 'Select' }).click();
    await page.getByRole('button', { name: 'Select all' }).click();
    await expect(page.locator('.topbar-selection-count')).toHaveText('3 selected');

    const copy = page.locator('#selection-bar').getByRole('button', { name: 'Copy to My Drive' });
    await expect(copy).toHaveCount(0);

    await page.getByRole('listitem', { name: /File: vault\.pdf/ }).click();
    await expect(page.locator('.topbar-selection-count')).toHaveText('2 selected');
    await expect(copy).toBeInViewport({ ratio: 1 });
    await copy.click();

    const dialog = page.getByRole('dialog', { name: 'Copy to My Drive' });
    await dialog.getByText('2 files selected', { exact: true }).click();
    for (const name of names) await expect(dialog.getByTitle(name, { exact: true })).toBeVisible();
    expect((await mock.calls('PreviewSourceCopyNames')).map(({ args }) => args)).toEqual([[names]]);
    expect(await mock.calls('SetActiveChannel')).toEqual([]);
});
