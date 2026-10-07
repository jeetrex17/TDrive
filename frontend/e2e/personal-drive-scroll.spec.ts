import { expect, resolves, byFirstArg, test, bootTDrive } from './wails-mock';

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

test.describe('scroll position memory', () => {
    test.use({ viewport: { width: 1280, height: 720 } });

    test('restores a folder scroll position when stepping back up to it', async ({ page }) => {
        // A folder-heavy root so a folder sits mid-viewport after scrolling: the
        // step forward must come from a row already on screen, or the test
        // runner would scroll the list to reach it and erase the offset first.
        const rootFolders = Array.from({ length: 60 }, (_, index) => ({
            id: `f${index}`, name: `Folder ${String(index).padStart(2, '0')}`, parent_id: '',
        }));
        await bootTDrive(page, {
            GetFolderContents: byFirstArg({
                '': resolves({ folders: rootFolders, files: [] }),
                f18: resolves({ folders: [], files: [
                    { name: 'inner.txt', size: 1, msg_id: 900, parent_id: 'f18', upload_time: 1, uploader_id: 7, encrypted: false, plaintext_size: 0 },
                ] }),
            }),
            GetFolderStats: resolves(rootFolders.map((folder) => ({ id: folder.id, bytes: 0, latestUpload: 0 }))),
        });

        const list = page.locator('#file-list');
        const folder = page.getByRole('row', { name: 'Folder: Folder 18' });
        await expect(page.getByRole('row', { name: 'Folder: Folder 00' })).toBeVisible();

        // Scroll until Folder 18 is on screen, then walk into it: the step
        // forward starts at the top.
        await list.evaluate((element) => { element.scrollTop = 500; });
        await expect(folder).toBeInViewport();
        const saved = await list.evaluate((element) => element.scrollTop);
        expect(saved).toBeGreaterThan(0);
        await folder.dblclick();
        await expect(page.getByRole('row', { name: 'File: inner.txt' })).toBeVisible();
        await expect.poll(() => list.evaluate((element) => element.scrollTop)).toBe(0);

        // Stepping back up restores where the folder was left.
        await page.locator('#breadcrumb-back').click();
        await expect(folder).toBeVisible();
        await expect.poll(() => list.evaluate((element) => element.scrollTop)).toBe(saved);
    });
});
