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
        { object_id: 'f:2615', kind: 'file', name: 'IMG_0042.HEIC', parent_path: 'Trips/Iceland', size: 4_812_000, deleted_at: now - 60_000, purge_after: now + 29 * DAY + HOUR },
        { object_id: 'f:2601', kind: 'file', name: 'budget.xlsx', parent_path: '', size: 12_000, deleted_at: now - 29 * DAY, purge_after: now + 6 * HOUR + 60_000 },
    ];
}

async function usePlatform(page: Page, platform: Platform): Promise<void> {
    if (platform === 'desktop') return;
    await page.setViewportSize({ width: 390, height: 844 });
    await page.addInitScript((mobile) => history.replaceState(null, '', `/?mobile=${mobile}`), platform);
}

/** Trash is a sidebar destination on the desktop and an Account row on a phone. */
async function openTrash(page: Page, platform: Platform) {
    if (platform === 'desktop') {
        await page.locator('#nav-trash').click();
    } else {
        await page.getByRole('navigation', { name: 'Primary' })
            .getByRole('button', { name: 'Account', exact: true }).click();
        await page.getByRole('button', { name: 'Trash', exact: true }).click();
    }
    return page.getByRole('dialog', { name: 'Trash', exact: true });
}

for (const platform of ['desktop', 'android'] as const) {
    test(`trash lists the newest deletion first with its origin and countdown on ${platform}`, async ({ page }, testInfo) => {
        await usePlatform(page, platform);
        const now = Date.now();
        await bootTDrive(page, { ListTrash: resolves(trashRows(now)), RestoreFromTrash: resolves(ok) });
        const dialog = await openTrash(page, platform);

        const rows = dialog.getByRole('listitem');
        await expect(rows).toHaveCount(3);
        // Newest deletion leads, whatever order the backend answered in.
        await expect(rows.first()).toContainText('IMG_0042.HEIC');
        await expect(rows.first()).toContainText('Trips/Iceland');
        await expect(rows.first()).toContainText('29 days left');
        // An item deleted from the drive's own root says so rather than nothing.
        await expect(rows.nth(2)).toContainText('Drive root');
        // Hours, not a timestamp, once the deadline is close.
        await expect(rows.nth(2)).toContainText('6 hours left');
        await expect(dialog).toContainText('3 items');
        await page.screenshot({ path: testInfo.outputPath(`trash-${platform}.png`) });

        await rows.first().getByRole('button', { name: 'Restore IMG_0042.HEIC' }).click();
        await expect(rows).toHaveCount(2);
        await expect(dialog).not.toContainText('IMG_0042.HEIC');
    });
}

for (const platform of ['desktop', 'android'] as const) {
    test(`permanent deletion asks before it happens on ${platform}`, async ({ page }) => {
        await usePlatform(page, platform);
        const mock = await bootTDrive(page, {
            ListTrash: resolves(trashRows(Date.now())),
            DeleteFromTrashPermanently: resolves(ok),
        });
        const dialog = await openTrash(page, platform);
        await dialog.getByRole('button', { name: 'Delete IMG_0042.HEIC permanently' }).click();

        const confirm = page.getByRole('dialog', { name: 'Delete permanently?' });
        await expect(confirm).toContainText("can't be undone");
        await expect(confirm).toContainText('IMG_0042.HEIC');
        // Backing out of the question deletes nothing.
        await confirm.getByRole('button', { name: 'Cancel', exact: true }).click();
        await expect(confirm).toBeHidden();
        expect(await mock.calls('DeleteFromTrashPermanently')).toHaveLength(0);
        await expect(dialog.getByRole('listitem')).toHaveCount(3);

        await dialog.getByRole('button', { name: 'Delete IMG_0042.HEIC permanently' }).click();
        await confirm.getByRole('button', { name: 'Delete permanently', exact: true }).click();
        await expect.poll(async () => (await mock.calls('DeleteFromTrashPermanently')).length).toBe(1);
        expect((await mock.calls('DeleteFromTrashPermanently'))[0].args).toEqual(['f:2615']);
        await expect(dialog.getByRole('listitem')).toHaveCount(2);
    });
}

test('emptying the trash confirms first and then leaves an empty state', async ({ page }) => {
    const mock = await bootTDrive(page, {
        ListTrash: resolves(trashRows(Date.now())),
        EmptyTrash: resolves(ok),
    });
    const dialog = await openTrash(page, 'desktop');
    await dialog.getByRole('button', { name: 'Empty trash', exact: true }).click();

    const confirm = page.getByRole('dialog', { name: 'Empty the trash?' });
    await expect(confirm).toContainText('3 items');
    await confirm.getByRole('button', { name: 'Empty trash', exact: true }).click();
    await expect.poll(async () => (await mock.calls('EmptyTrash')).length).toBe(1);

    await expect(dialog).toContainText('Nothing in the trash');
    // Nothing left to empty, so the control is gone rather than sitting disabled.
    await expect(dialog.getByRole('button', { name: 'Empty trash', exact: true })).toHaveCount(0);
});

test('an empty trash explains itself instead of showing a bare list', async ({ page }) => {
    await bootTDrive(page, { ListTrash: resolves([]) });
    const dialog = await openTrash(page, 'desktop');
    await expect(dialog).toContainText('Nothing in the trash');
    await expect(dialog.getByRole('listitem')).toHaveCount(0);
});

test("a refused restore repeats the backend's reason", async ({ page }) => {
    await bootTDrive(page, {
        ListTrash: resolves(trashRows(Date.now())),
        RestoreFromTrash: resolves({ ok: false, error: { code: 'operation_failed', message: 'The original folder is gone.' } }),
    });
    const dialog = await openTrash(page, 'desktop');
    await dialog.getByRole('button', { name: 'Restore IMG_0042.HEIC' }).click();
    await expect(dialog.getByRole('alert')).toHaveText('The original folder is gone.');
    // A refusal keeps the row: it is still in the trash.
    await expect(dialog.getByRole('listitem')).toHaveCount(3);
});

test('a trash that cannot be read offers a way to try again', async ({ page }) => {
    const mock = await bootTDrive(page, { ListTrash: resolves(null) });
    await mock.setPlan('ListTrash', rejects('Telegram is unreachable.'));
    const dialog = await openTrash(page, 'desktop');
    await expect(dialog).toContainText('The trash could not be opened');
    await mock.setPlan('ListTrash', resolves(trashRows(Date.now())));
    await dialog.getByRole('button', { name: 'Try again', exact: true }).click();
    await expect(dialog.getByRole('listitem')).toHaveCount(3);
});
