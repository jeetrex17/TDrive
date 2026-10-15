import type { Page } from '@playwright/test';
import { bootTDrive, byFirstArg, deferred, expect, resolves, test } from './wails-mock';

async function chooseUpload(page: Page, mobile = false): Promise<void> {
    if (mobile) {
        await page.getByRole('group', { name: 'Files', exact: true }).getByRole('button', { name: 'Upload files', exact: true }).click();
        return;
    }
    await page.locator('#upload-btn').click();
    await page.locator('#upload-menu-files').click();
}

for (const platform of ['desktop', 'android'] as const) {
    test(`${platform}: a confirmed upload waits behind the active batch`, async ({ page }) => {
        if (platform === 'android') await page.setViewportSize({ width: 390, height: 844 });
        const mock = await bootTDrive(page, {
            SelectFiles: resolves(['/tmp/first.txt']),
            GetFolderContents: byFirstArg({ '': resolves({
                folders: platform === 'desktop' ? [{ id: 'reports', name: 'Reports', parent_id: '' }] : [], files: [],
            }) }, resolves({ folders: [], files: [] })),
            PlanImport: resolves({ files: 1, folders: 0, archives: 0, limitExceeded: false }),
            RetryUploadToDriveFS: deferred('first', { result: { ok: true }, files: [{ name: 'first.txt', msg_id: 41 }], failures: [] }),
        }, { url: platform === 'android' ? '/?mobile=android' : '/' });
        await expect(page.locator('#success-screen')).toBeVisible();
        await chooseUpload(page, platform === 'android');
        await page.getByRole('button', { name: 'Continue', exact: true }).click();
        await expect.poll(() => mock.calls('RetryUploadToDriveFS')).toMatchObject([
            { args: [['/tmp/first.txt'], [''], false, 1], state: 'pending' },
        ]);

        await mock.setPlan('SelectFiles', resolves(['/tmp/queued.txt']));
        await mock.setPlan('RetryUploadToDriveFS', deferred('second', {
            result: { ok: true }, files: [{ name: 'queued.txt', msg_id: 42 }], failures: [],
        }));
        if (platform === 'desktop') await page.getByRole('row', { name: 'Folder: Reports', exact: true }).dblclick();
        await chooseUpload(page, platform === 'android');
        const review = page.locator('#upload-options-modal').getByRole('dialog');
        await expect(review).toContainText('queued.txt');
        await expect(review).toContainText('Personal');
        if (platform === 'desktop') await expect(review).toContainText('Reports');
        await review.getByRole('button', { name: 'Add to queue', exact: true }).click();
        await expect(review).toBeHidden();
        if (platform === 'desktop') await page.locator('#breadcrumb-back').click();

        if (platform === 'desktop') await page.locator('#notif-bell').click();
        else {
            await page.getByRole('navigation', { name: 'Primary' }).getByRole('button', { name: /^Transfers/ }).click();
            await page.getByRole('button', { name: 'View all activity', exact: true }).click();
        }
        const activity = page.getByRole('dialog', { name: 'Activity', exact: true });
        await activity.getByRole('button', { name: 'View queued.txt', exact: true }).click();
        const details = activity.getByRole('region', { name: 'Details for queued.txt', exact: true });
        await expect(details).toContainText('Waiting its turn');
        await expect(details).toContainText('Personal');
        if (platform === 'desktop') await expect(details).toContainText('Reports');
        await expect(details).toContainText('Not encrypted');
        await expect(details).toContainText('queued.txt');
        expect(await details.evaluate((element) => element.scrollWidth <= element.clientWidth + 1)).toBe(true);
        await activity.screenshot({ path: test.info().outputPath(`queued-upload-${platform}.png`) });
        // A visible queued row proves enqueue processing has settled. A second
        // backend call here would overlap the two batches and risk reused IDs.
        expect(await mock.calls('RetryUploadToDriveFS')).toHaveLength(1);
        await activity.getByRole('button', { name: 'Close activity', exact: true }).click();
        await mock.release('first');
        await expect.poll(() => mock.calls('RetryUploadToDriveFS')).toMatchObject([
            { args: [['/tmp/first.txt'], [''], false, 1], state: 'fulfilled' },
            { args: [['/tmp/queued.txt'], [platform === 'desktop' ? 'reports' : ''], false, 1], state: 'pending' },
        ]);
        await mock.release('second');
        await expect.poll(async () => (await mock.calls('RetryUploadToDriveFS')).map(({ state }) => state)).toEqual(['fulfilled', 'fulfilled']);
    });
}

test('large selections keep the confirmation controls on screen', async ({ page }) => {
    await page.setViewportSize({ width: 1100, height: 700 });
    const paths = Array.from({ length: 60 }, (_, index) => `/tmp/project-notes-${index}.txt`);
    await bootTDrive(page, {
        SelectFiles: resolves(paths),
        PlanImport: resolves({ files: 60, folders: 0, archives: 0, limitExceeded: false }),
    });
    await expect(page.locator('#success-screen')).toBeVisible();
    await chooseUpload(page);
    const review = page.locator('#upload-options-modal').getByRole('dialog');
    await expect(review.getByRole('list', { name: 'Selected files' }).getByRole('listitem')).toHaveCount(50);
    await expect(review.getByRole('button', { name: 'Continue', exact: true })).toBeInViewport();
    await expect(review.getByRole('button', { name: 'Cancel', exact: true })).toBeInViewport();
});

test('removing a waiting upload leaves the active batch running', async ({ page }) => {
    const mock = await bootTDrive(page, {
        SelectFiles: resolves(['/tmp/first.txt']),
        PlanImport: resolves({ files: 1, folders: 0, archives: 0, limitExceeded: false }),
        RetryUploadToDriveFS: deferred('active', { result: { ok: true }, files: [{ name: 'first.txt', msg_id: 41 }], failures: [] }),
        CancelUpload: resolves(null),
        ReleaseUploadSources: resolves(null),
    });
    await expect(page.locator('#success-screen')).toBeVisible();
    await chooseUpload(page);
    await page.getByRole('button', { name: 'Continue', exact: true }).click();
    await expect.poll(async () => (await mock.calls('RetryUploadToDriveFS')).length).toBe(1);
    await mock.setPlan('SelectFiles', resolves(['/tmp/removed.txt']));
    await chooseUpload(page);
    await page.getByRole('button', { name: 'Add to queue', exact: true }).click();
    await page.locator('#notif-bell').click();
    const activity = page.getByRole('dialog', { name: 'Activity', exact: true });
    await activity.getByRole('button', { name: 'View removed.txt', exact: true }).click();
    const details = activity.getByRole('region', { name: 'Details for removed.txt', exact: true });
    await details.getByRole('button', { name: 'Remove from queue', exact: true }).click();
    await expect(details).toContainText('Canceled');
    await expect.poll(() => mock.calls('ReleaseUploadSources')).toMatchObject([{ args: [['/tmp/removed.txt']], state: 'fulfilled' }]);
    expect(await mock.calls('CancelUpload')).toEqual([]);
    expect(await mock.calls('RetryUploadToDriveFS')).toMatchObject([{ state: 'pending' }]);
    await mock.release('active');
    await activity.getByRole('button', { name: 'View first.txt', exact: true }).click();
    await expect(activity.getByRole('region', { name: 'Details for first.txt', exact: true })).toContainText('Completed');
    expect(await mock.calls('RetryUploadToDriveFS')).toMatchObject([{ state: 'fulfilled' }]);
});

test('Android keeps the native folder selection until its upload has released the source', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await page.addInitScript(() => {
        const host = window as unknown as {
            wails: Record<string, (...args: string[]) => void>;
            _wailsAndroidCallback?: (id: string, result: string, error: null) => void;
            __folderBridgeCalls: { picks: number; released: string[][] };
        };
        host.__folderBridgeCalls = { picks: 0, released: [] };
        host.wails = {
            pickFolder(id) {
                host.__folderBridgeCalls.picks += 1;
                host._wailsAndroidCallback?.(id, JSON.stringify({ root: 'Holiday', files: [{ id: 'doc:0', rel: 'a.txt', size: 5 }] }), null);
            },
            materializeFiles(id) {
                host._wailsAndroidCallback?.(id, JSON.stringify({ paths: { 'doc:0': '/cache/a.txt' } }), null);
            },
            releaseFiles(id, selected) {
                host.__folderBridgeCalls.released.push(JSON.parse(selected) as string[]);
                host._wailsAndroidCallback?.(id, '', null);
            },
        };
    });
    const mock = await bootTDrive(page, {
        CreateFolderForDrive: resolves({ id: 'holiday', name: 'Holiday', parent_id: '' }),
        RetryUploadToDriveFS: deferred('folder', { result: { ok: true }, files: [{ name: 'a.txt', msg_id: 41 }], failures: [] }),
    }, { url: '/?mobile=android' });
    const uploadFolder = page.getByRole('group', { name: 'Files', exact: true }).getByRole('button', { name: 'Upload folder', exact: true });
    await uploadFolder.click();
    await page.locator('#import-options-confirm').click();
    await expect.poll(() => mock.calls('RetryUploadToDriveFS')).toMatchObject([
        { args: [['/cache/a.txt'], ['holiday'], false, 1], state: 'pending' },
    ]);
    await uploadFolder.click();
    await expect(page.locator('.toast').filter({ hasText: 'A folder is already selected' })).toBeVisible();
    await expect(page.locator('#import-options-modal').getByRole('dialog')).toBeHidden();
    expect(await page.evaluate(() => (window as unknown as { __folderBridgeCalls: { picks: number } }).__folderBridgeCalls.picks)).toBe(1);
    expect(await mock.calls('RetryUploadToDriveFS')).toHaveLength(1);
    await mock.release('folder');
    await expect.poll(() => page.evaluate(() => (window as unknown as { __folderBridgeCalls: { released: string[][] } }).__folderBridgeCalls.released)).toEqual([['doc:0']]);
    await uploadFolder.click();
    await expect(page.locator('#import-options-modal').getByRole('dialog')).toBeVisible();
    expect(await page.evaluate(() => (window as unknown as { __folderBridgeCalls: { picks: number } }).__folderBridgeCalls.picks)).toBe(2);
    await page.locator('#import-options-cancel').click();
});
