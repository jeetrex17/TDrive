import { bootTDrive, expect, rejects, resolves, test } from './wails-mock';
import type { Page } from '@playwright/test';

type Platform = 'desktop' | 'android';

const HOUR = 3_600_000;
const DAY = 24 * HOUR;
const ok = { ok: true };

/** Fixed offsets from the moment the page loads, so the countdowns are stable. */
function trashRows(now: number) {
    return [
        { object_id: 'd:9c1', kind: 'folder', name: 'Tax returns 2024', parent_path: 'Documents', size: 0, deleted_at: now - 3 * DAY, purge_after: now + 27 * DAY + HOUR },
        { object_id: 'f:2615', kind: 'file', name: 'IMG_0042.jpg', parent_path: 'Trips/Iceland', size: 4_812_000, deleted_at: now - 60_000, purge_after: now + 29 * DAY + HOUR, revision: 4, },
        { object_id: 'f:2601', kind: 'file', name: 'budget.xlsx', parent_path: '', size: 12_000, deleted_at: now - 29 * DAY, purge_after: now + 6 * HOUR + 60_000, revision: 2, },
    ];
}

async function usePlatform(page: Page, platform: Platform): Promise<void> {
    if (platform === 'desktop') return;
    await page.setViewportSize({ width: 390, height: 844 });
    await page.addInitScript((mobile) => history.replaceState(null, '', `/?mobile=${mobile}`), platform);
}

/**
 * Trash is a sidebar destination on the desktop and an Account row on a phone.
 * Either way it lands in the drive's own file list rather than in a dialog, so
 * what comes back is the list every other folder is read from.
 */
async function openTrash(page: Page, platform: Platform) {
    if (platform === 'desktop') {
        await page.locator('#nav-trash').click();
    } else {
        await page.getByRole('navigation', { name: 'Primary' })
            .getByRole('button', { name: 'Account', exact: true }).click();
        await page.getByRole('button', { name: 'Trash', exact: true }).click();
    }
    return page.locator('#file-list');
}

for (const platform of ['desktop', 'android'] as const) {
    test(`trash lists the newest deletion first with its origin and countdown on ${platform}`, async ({ page }, testInfo) => {
        await usePlatform(page, platform);
        const now = Date.now();
        await bootTDrive(page, { ListTrash: resolves(trashRows(now)), RestoreFromTrash: resolves(ok) });
        const list = await openTrash(page, platform);

        const rows = list.locator('.drive-row');
        await expect(rows).toHaveCount(3);
        // This is the drive's list, so it keeps the drive's convention of
        // folders above files. Within each group the newest deletion leads,
        // because it has the most time left and the list sorts on the deadline
        // the countdown column is showing.
        await expect(rows.nth(0)).toContainText('Tax returns 2024');
        await expect(rows.nth(1)).toContainText('IMG_0042.jpg');
        await expect(rows.nth(1)).toHaveAttribute('aria-label', /Trips\/Iceland/);
        await expect(rows.nth(1)).toContainText('29 days left');
        // An item deleted from the drive's own root says so rather than nothing.
        await expect(rows.nth(2)).toHaveAttribute('aria-label', /Drive root/);
        // Hours, not a timestamp, once the deadline is close.
        await expect(rows.nth(2)).toContainText('6 hours left');
        await page.screenshot({ path: testInfo.outputPath(`trash-${platform}.png`), fullPage: true });

        await rows.nth(1).getByRole('button', { name: 'Restore IMG_0042.jpg' }).click();
        await expect(rows).toHaveCount(2);
        await expect(list).not.toContainText('IMG_0042.jpg');
    });
}

for (const platform of ['desktop', 'android'] as const) {
    test(`permanent deletion asks before it happens on ${platform}`, async ({ page }) => {
        await usePlatform(page, platform);
        const mock = await bootTDrive(page, {
            ListTrash: resolves(trashRows(Date.now())),
            DeleteFromTrashPermanently: resolves(ok),
        });
        const list = await openTrash(page, platform);
        const rows = list.locator('.drive-row');
        await rows.nth(1).getByRole('button', { name: 'Delete IMG_0042.jpg forever' }).click();

        const confirm = page.getByRole('dialog', { name: 'Delete permanently?' });
        await expect(confirm).toContainText("can't be undone");
        await expect(confirm).toContainText('IMG_0042.jpg');
        // Backing out of the question deletes nothing.
        await confirm.getByRole('button', { name: 'Cancel', exact: true }).click();
        await expect(confirm).toBeHidden();
        expect(await mock.calls('DeleteFromTrashPermanently')).toHaveLength(0);
        await expect(rows).toHaveCount(3);

        await rows.nth(1).getByRole('button', { name: 'Delete IMG_0042.jpg forever' }).click();
        await confirm.getByRole('button', { name: 'Delete permanently', exact: true }).click();
        await expect.poll(async () => (await mock.calls('DeleteFromTrashPermanently')).length).toBe(1);
        expect((await mock.calls('DeleteFromTrashPermanently'))[0].args).toEqual(['f:2615']);
        await expect(rows).toHaveCount(2);
    });
}

test('emptying the trash confirms first and then leaves an empty state', async ({ page }) => {
    const mock = await bootTDrive(page, {
        ListTrash: resolves(trashRows(Date.now())),
        EmptyTrash: resolves(ok),
    });
    const list = await openTrash(page, 'desktop');
    await page.locator('#trash-empty-btn').click();

    const confirm = page.getByRole('dialog', { name: 'Empty the trash?' });
    await expect(confirm).toContainText('3 items');
    await confirm.getByRole('button', { name: 'Empty trash', exact: true }).click();
    await expect.poll(async () => (await mock.calls('EmptyTrash')).length).toBe(1);

    await expect(list).toContainText('Nothing in the trash');
    // Nothing left to empty, so the control is gone rather than sitting disabled.
    await expect(page.locator('#trash-empty-btn')).toHaveCount(0);
});

test('an empty trash explains itself instead of showing a bare list', async ({ page }) => {
    await bootTDrive(page, { ListTrash: resolves([]) });
    const list = await openTrash(page, 'desktop');
    await expect(list).toContainText('Nothing in the trash');
    await expect(list.locator('.drive-row')).toHaveCount(0);
});

test('a refused restore says so without throwing away a list that is still right', async ({ page }) => {
    await bootTDrive(page, {
        ListTrash: resolves(trashRows(Date.now())),
        RestoreFromTrash: resolves({ ok: false, error: { code: 'operation_failed', message: 'The original folder is gone.' } }),
    });
    const list = await openTrash(page, 'desktop');
    await list.locator('.drive-row').nth(1).getByRole('button', { name: 'Restore IMG_0042.jpg' }).click();
    await expect(page.locator('.toast').filter({ hasText: 'The original folder is gone.' })).toBeVisible();
    // A refusal keeps the row: it is still in the trash.
    await expect(list.locator('.drive-row')).toHaveCount(3);
});

test('a trash that cannot be read offers a way to try again', async ({ page }) => {
    const mock = await bootTDrive(page, { ListTrash: resolves(null) });
    await mock.setPlan('ListTrash', rejects('Telegram is unreachable.'));
    const list = await openTrash(page, 'desktop');
    await expect(list).toContainText('The trash could not be opened');
    await mock.setPlan('ListTrash', resolves(trashRows(Date.now())));
    // 'Retry' is what the drive's own failed folder load offers; the trash is
    // that list now, so it says the same word.
    await list.getByRole('button', { name: 'Retry', exact: true }).click();
    await expect(list.locator('.drive-row')).toHaveCount(3);
});

test('the trash reads as the drive: same header, no folder trail, nothing to upload into', async ({ page }) => {
    await bootTDrive(page, { ListTrash: resolves(trashRows(Date.now())) });
    await openTrash(page, 'desktop');

    await expect(page.locator('#trash-title')).toBeVisible();
    // The drive's own column header is still there, because this is the drive's
    // own list -- but the date column is reporting the countdown instead.
    await expect(page.locator('.file-table-header')).toBeVisible();
    await expect(page.locator('.file-table-header')).toContainText('Time left');
    // No folder to be in, so no trail and no way back up.
    await expect(page.locator('.breadcrumb-path')).toBeHidden();
    // Nothing can be added to the trash.
    await expect(page.locator('.upload-menu-wrap')).toBeHidden();
});

test('a trashed row offers only the two things a deleted item can do', async ({ page }) => {
    await bootTDrive(page, { ListTrash: resolves(trashRows(Date.now())) });
    const list = await openTrash(page, 'desktop');
    const row = list.locator('.drive-row').nth(1);

    await expect(row.getByRole('button', { name: 'Restore IMG_0042.jpg' })).toBeVisible();
    await expect(row.getByRole('button', { name: 'Delete IMG_0042.jpg forever' })).toBeVisible();
    // Nothing that acts on a live file.
    await expect(row.locator('button.download')).toHaveCount(0);
    await expect(row.locator('button.open-file')).toHaveCount(0);

    // A deleted item cannot be selected into the drive's bulk actions, and a
    // right-click offers no menu of operations it could not perform anyway.
    await row.click();
    await expect(page.locator('.selection-bar')).toBeHidden();
    await row.click({ button: 'right' });
    await expect(page.getByRole('menu')).toHaveCount(0);
});

test('leaving the trash returns the drive to the folder it was showing', async ({ page }) => {
    await bootTDrive(page, { ListTrash: resolves(trashRows(Date.now())) });
    await openTrash(page, 'desktop');
    await expect(page.locator('#trash-title')).toBeVisible();

    // The sidebar drive is the way back, the same as it is out of Photos.
    await page.locator('.drive-item[data-channel-id]').first().click();
    await expect(page.locator('#trash-title')).toBeHidden();
    await expect(page.locator('.breadcrumb-path')).toBeVisible();
    await expect(page.locator('.file-table-header')).toContainText('Date');
});

/**
 * Three ways the drive used to leak into the trash. A deleted folder is a real
 * folder row with a real id, so the folder menu -- open, upload into, rename,
 * delete -- would have acted on it as if it were still in the drive; an OS
 * file drop started an upload into the folder behind the trash's chrome; and
 * a search drew live results under the "Trash" title with Empty trash beside
 * them.
 */
test('the trash offers no folder menu, takes no drop, and a search leaves it', async ({ page }) => {
    await bootTDrive(page, {
        ListTrash: resolves(trashRows(Date.now())),
        GetFolderContents: resolves({ folders: [], files: [] }),
        Search: resolves([{ msg_id: 77, name: 'a.txt', size: 12, parent_id: '', upload_time: 1, uploader_id: 7, encrypted: false, plaintext_size: 0, path: 'a.txt' }]),
    });
    const list = await openTrash(page, 'desktop');
    await expect(list.getByRole('row', { name: /Tax returns 2024/ })).toBeVisible();

    await list.getByRole('row', { name: /Tax returns 2024/ }).click({ button: 'right' });
    await expect(page.locator('#context-menu')).not.toHaveClass(/open|visible/);
    await expect(page.getByRole('menuitem', { name: /Upload files to this folder/ })).toHaveCount(0);

    await page.fill('#search-input', 'a');
    await expect(page.locator('.main-content')).not.toHaveClass(/trash-mode/);
    await expect(page.locator('#trash-empty-btn')).toBeHidden();
});

test('on a phone the trash has its own bar and no upload button', async ({ page }) => {
    await usePlatform(page, 'android');
    await bootTDrive(page, { ListTrash: resolves(trashRows(Date.now())) });
    await openTrash(page, 'android');
    const bar = page.locator('.topbar-trash');
    await expect(bar.getByRole('heading', { name: 'Trash' })).toBeVisible();
    await expect(bar).toContainText('3 items');
    await expect(bar.getByRole('button', { name: 'Empty' })).toBeVisible();
    await expect(page.locator('.topbar-files')).toBeHidden();
    await expect(page.locator('.mobile-fab')).toBeHidden();
    // The chevron is the way out, and it goes back to the drive.
    await bar.getByRole('button', { name: 'Back' }).click();
    await expect(page.locator('.main-content')).not.toHaveClass(/trash-mode/);
    await expect(page.locator('.topbar-files')).toBeVisible();
});
