import { bootTDrive, expect, resolves, test } from './wails-mock';
import type { Page } from '@playwright/test';

type Platform = 'desktop' | 'android' | 'ios';

/**
 * The panel is a page of its own on both: behind the profile menu on a desktop
 * and behind an Account row on a phone, where it used to unfold at the bottom
 * of the account list.
 */
async function openPanel(page: Page, platform: Platform) {
    if (platform === 'desktop') {
        await page.locator('#profile-trigger').click();
        await page.getByRole('menuitem', { name: 'Photo & video backup' }).click();
    } else {
        await page.getByRole('navigation', { name: 'Primary' }).getByRole('button', { name: 'Account', exact: true }).click();
        await page.getByRole('button', { name: /Photo & video backup/ }).click();
    }
    return page.getByRole('region', { name: 'Photo and video backup' });
}

async function usePlatform(page: Page, platform: Platform): Promise<void> {
    if (platform === 'desktop') return;
    await page.setViewportSize({ width: 390, height: 844 });
    await page.addInitScript((mobile) => history.replaceState(null, '', `/?mobile=${mobile}`), platform);
}

const ok = { ok: true };

/**
 * Nothing in the panel paints past its own edge.
 *
 * Measured by what is drawn rather than by scrollWidth: a 44px tap target with
 * a 16px glyph centred in it reports a scroll box a few pixels wider than its
 * client box, which is not something anyone can see and not what this is
 * asking about.
 */
async function overflowsSideways(panel: ReturnType<Page['getByRole']>): Promise<boolean> {
    return panel.evaluate((element) => {
        const box = element.getBoundingClientRect();
        return [...element.querySelectorAll('*')].some((node) => {
            const child = node.getBoundingClientRect();
            return child.width > 0 && (child.right > box.right + 1 || child.left < box.left - 1);
        });
    });
}


for (const platform of ['desktop', 'android'] as const) {
    test(`locked backup opens the password prompt on ${platform} and cancel keeps it stopped`, async ({ page }) => {
        await usePlatform(page, platform);
        const mock = await bootTDrive(page, {
            GetPhotoBackupState: resolves({
                platform, encryption_required: true,
                settings: { enabled: true, photos: true, videos: true, encrypt: true },
                sources: [{ id: 'camera', name: 'Camera', enabled: true }],
                status: { phase: 'paused', pending: 12, message: 'Unlock encryption to back up your photos and videos.' },
            }),
            EncryptionStatus: resolves({ available: true, password_set: true, password_remembered: false, hint: '' }),
        });
        const panel = await openPanel(page, platform);
        await expect(panel).toContainText('Unlock to continue');
        await panel.getByRole('button', { name: 'Unlock and back up', exact: true }).click();
        const dialog = page.getByRole('dialog', { name: 'Enter encryption password' });
        await expect(dialog).toBeVisible();
        expect(await mock.calls('RunPhotoBackup')).toHaveLength(0);
        await dialog.getByRole('button', { name: 'Cancel', exact: true }).click();
        await expect(dialog).toBeHidden();
        expect(await mock.calls('RunPhotoBackup')).toHaveLength(0);
        // Dismissing the prompt is a choice, not a failure: nothing turns red.
        await expect(panel.getByRole('alert')).toHaveCount(0);
    });
}

test("a refused start repeats the backend's reason instead of a generic failure", async ({ page }) => {
    const mock = await bootTDrive(page, {
        GetPhotoBackupState: resolves({
            platform: 'darwin',
            settings: { enabled: true, photos: true, videos: true, wifi_only: true },
            sources: [{ id: 'pictures', kind: 'folder', name: 'Pictures', enabled: true }],
            status: { phase: 'idle', complete: 3 },
        }),
        RunPhotoBackup: resolves({ ok: false, error: { code: 'operation_failed', message: 'Waiting for Wi-Fi.' } }),
    });
    const panel = await openPanel(page, 'desktop');
    // A watched folder is walked all the way down, so the panel says so.
    await expect(panel).toContainText('Subfolders are backed up too');
    await panel.getByRole('button', { name: 'Back up now', exact: true }).click();
    await expect.poll(async () => (await mock.calls('RunPhotoBackup')).length).toBe(1);
    await expect(panel.getByRole('alert')).toHaveText('Waiting for Wi-Fi.');
    await expect(page.getByRole('dialog', { name: 'Enter encryption password' })).toHaveCount(0);
});

test('empty backup settings fit the desktop viewport and guide source selection', async ({ page }, testInfo) => {
    await page.setViewportSize({ width: 1024, height: 768 });
    await bootTDrive(page, {
        GetPhotoBackupState: resolves({
            platform: 'darwin', manual_paused: false,
            settings: { enabled: true, photos: true, videos: true }, sources: [],
            status: { phase: 'idle' }, destination: { title: 'Photo backup' },
            capabilities: {
                wifi_only: { supported: false, label: 'Wi-Fi only' },
                access: { status: 'available', detail: 'Photo access is managed by the device.' },
            },
        }),
    });
    const panel = await openPanel(page, 'desktop');
    await expect(panel).toContainText('Choose what to back up');
    await expect(panel.getByRole('button', { name: 'Add folder', exact: true })).toBeVisible();
    // Nothing to start yet, so no start button to sit there disabled.
    await expect(panel.getByRole('button', { name: 'Back up now', exact: true })).toHaveCount(0);
    // Wi-Fi is a phone condition; a desktop never shows a switch it cannot honour.
    await expect(panel.getByRole('switch', { name: 'Wi-Fi only' })).toHaveCount(0);
    // Full access is the expected case and earns no line of copy.
    await expect(panel).not.toContainText('managed by the device');
    const bounds = await page.locator('#profile-menu').boundingBox();
    expect(bounds).not.toBeNull();
    expect(bounds!.x).toBeGreaterThanOrEqual(0);
    expect(bounds!.y + bounds!.height).toBeLessThanOrEqual(768);
    await page.screenshot({ path: testInfo.outputPath('backup-desktop-empty.png') });
});

for (const platform of ['desktop', 'android', 'ios'] as const) {
    test(`photo backup settings and pause are available on ${platform}`, async ({ page }, testInfo) => {
        await usePlatform(page, platform);
        const state = {
            platform,
            manual_paused: false,
            settings: { enabled: true, photos: true, videos: true, wifi_only: false },
            sources: [{ id: 'camera', kind: 'library', root: 'Camera', name: 'Camera', enabled: true, added_at: 1 }],
            status: { phase: 'uploading', pending: 12, uploading: 1, complete: 24, failed: 0, current_file: 'Trips/IMG_0042.HEIC', current_file_bytes_done: 500, current_file_bytes_total: 1000, current_file_percent: 50 },
            capabilities: {
                wifi_only: { supported: platform === 'android', label: 'Wi-Fi only', detail: 'Requires a recent device connectivity update.' },
                access: { status: 'limited', detail: 'Only selected photos are accessible.' },
            },
        };
        const mock = await bootTDrive(page, {
            GetPhotoBackupState: resolves(state),
            PausePhotoBackup: resolves(ok),
            ResumePhotoBackup: resolves(ok),
        });
        const panel = await openPanel(page, platform);
        await expect(panel.getByRole('switch', { name: 'Back up photos & videos' })).toHaveAttribute('aria-checked', 'true');
        await expect(panel.getByRole('switch', { name: 'Photos', exact: true })).toHaveAttribute('aria-checked', 'true');
        await expect(panel.getByRole('switch', { name: 'Videos', exact: true })).toHaveAttribute('aria-checked', 'true');
        await expect(panel.getByRole('switch', { name: 'Wi-Fi only' })).toHaveCount(platform === 'android' ? 1 : 0);
        await expect(panel).toContainText('Only selected photos are accessible');
        // A photo library is a flat set of resources; nothing to mirror.
        await expect(panel).not.toContainText('Subfolders are backed up too');
        await expect(panel).toContainText('Backing up');
        await expect(panel).toContainText('IMG_0042.HEIC');
        await expect(panel).toContainText('24 backed up · 13 waiting');
        // The bar answers "how far through the backup", not "how far through
        // this file": 24 of the 37 items the queue is holding are done. The
        // file it is on is named above it.
        await expect(panel.getByRole('progressbar')).toHaveAttribute('aria-valuenow', '24');
        await expect(panel.getByRole('progressbar')).toHaveAttribute('aria-valuemax', '37');
        expect(await overflowsSideways(panel)).toBe(false);
        await page.screenshot({ path: testInfo.outputPath(`backup-${platform}.png`), fullPage: true });

        await mock.setPlan('GetPhotoBackupState', resolves({
            ...state, manual_paused: true, status: { ...state.status, phase: 'paused', uploading: 0, pending: 13, current_file: '', message: 'Paused by you.' },
        }));
        await panel.getByRole('button', { name: 'Pause', exact: true }).click();
        await expect.poll(async () => (await mock.calls('PausePhotoBackup')).length).toBe(1);
        await expect(panel).toContainText('Paused');
        await expect(panel).toContainText('24 backed up · 13 waiting');
        await expect(panel.getByRole('button', { name: 'Resume', exact: true })).toBeVisible();
        await mock.emit('photo-backup:state');
        await expect(panel.getByRole('button', { name: 'Resume', exact: true })).toBeVisible();
        expect(await mock.calls('ResumePhotoBackup')).toHaveLength(0);
        await panel.getByRole('button', { name: 'Resume', exact: true }).click();
        await expect.poll(async () => (await mock.calls('ResumePhotoBackup')).length).toBe(1);
        await mock.setPlan('GetPhotoBackupState', resolves(state));
        await mock.emit('photo-backup:state');
        await expect(panel.getByRole('button', { name: 'Pause', exact: true })).toBeVisible();
        expect(await mock.calls('ClearGalleryCache')).toHaveLength(0);
    });
}

for (const platform of ['desktop', 'android', 'ios'] as const) {
    test(`backup notifications show live current-file progress on ${platform}`, async ({ page }, testInfo) => {
        await usePlatform(page, platform);
        const state = {
            platform, settings: { enabled: true, photos: true, videos: true },
            sources: [{ id: 'camera', name: 'Camera', enabled: true }],
            status: { phase: 'uploading', complete: 1, pending: 30, uploading: 1,
                current_file: 'holiday.jpg', current_file_bytes_done: 500000,
                current_file_bytes_total: 1000000, current_file_percent: 50 },
        };
        const mock = await bootTDrive(page, { GetPhotoBackupState: resolves(state) });
        if (platform === 'desktop') {
            // The bell's name carries its state ("Notifications, 1 transfer in
            // progress"), so match the stable prefix rather than a snapshot of it.
            await page.getByRole('button', { name: /^Notifications/ }).click();
        } else {
            await page.getByRole('navigation', { name: 'Primary' }).getByRole('button', { name: 'Transfers', exact: true }).click();
        }
        const notifications = platform === 'desktop' ? page.getByRole('dialog', { name: 'Notifications', exact: true }) : page.locator('.transfers-tab');
        // The transfer rows themselves, not the per-file lines nested inside
        // them: the point of the count is that one backup is one row.
        const rows = platform === 'desktop' ? notifications.locator('.notif-row-transfer') : notifications.locator('.row[role="listitem"]');
        await expect(notifications).toContainText('holiday.jpg');
        // Two bars now, and they answer different questions: the row's is how
        // far through the backup, the file's is how far through that file. The
        // run is 1 of 31 done, so the aggregate is not the file's 50%.
        await expect(notifications.getByRole('progressbar', { name: 'holiday.jpg, 50%' }))
            .toHaveAttribute('aria-valuenow', '50');
        await expect(notifications.getByRole('progressbar', { name: /Uploading Photo backup/ }))
            .toHaveAttribute('aria-valuenow', '3');
        await expect(rows).toHaveCount(1);
        await mock.setPlan('GetPhotoBackupState', resolves({ ...state, status: { ...state.status,
            complete: 2, pending: 29, current_file: 'birthday.mp4',
            current_file_bytes_done: 750000, current_file_percent: 75 } }));
        await mock.emit('photo-backup:state');
        await expect(notifications).toContainText('birthday.mp4');
        await expect(notifications.getByRole('progressbar', { name: 'birthday.mp4, 75%' }))
            .toHaveAttribute('aria-valuenow', '75');
        // One row still, and one file listed under it: a camera roll must not
        // become an equally long notification log.
        await expect(rows).toHaveCount(1);
        expect(await notifications.evaluate((element) => element.scrollWidth <= element.clientWidth + 1)).toBe(true);
        await page.screenshot({ path: testInfo.outputPath(`backup-progress-${platform}.png`) });
    });
}

/**
 * The panel on a 390px screen. Every line here used to fight for the same row:
 * "Ready to back up" wrapped onto two while its counts were cut off beside it,
 * and "Backing up" wrapped under two buttons staggering down the side of it.
 */
test('the phone panel gives each line its own row and the action the full card', async ({ page }) => {
    await usePlatform(page, 'android');
    await bootTDrive(page, {
        GetPhotoBackupState: resolves({
            platform: 'android',
            settings: { enabled: true, photos: true, videos: true, encrypt: true },
            sources: [{ id: 'camera', kind: 'android', name: 'Camera', root: '/DCIM/Camera', enabled: true }],
            status: { phase: 'queued', complete: 41, pending: 14 },
            destination: { title: 'Photo backup / Pixel 8 / Camera' },
        }),
    });
    const panel = await openPanel(page, 'android');
    await expect(panel).toBeVisible();

    // The count is under the state, in full, not cut off beside it.
    const title = panel.getByText('Ready to back up', { exact: true });
    const summary = panel.getByText('41 backed up · 14 waiting', { exact: true });
    const titleBox = (await title.boundingBox())!;
    const summaryBox = (await summary.boundingBox())!;
    expect(summaryBox.y).toBeGreaterThanOrEqual(titleBox.y + titleBox.height - 1);
    expect(await summary.evaluate((node) => node.scrollWidth <= node.clientWidth + 1)).toBe(true);

    // The action runs the width of the card it sits in.
    const card = (await panel.locator('.pb-situation').boundingBox())!;
    const action = (await panel.getByRole('button', { name: 'Back up now', exact: true }).boundingBox())!;
    expect(card.width - action.width).toBeLessThan(40);

    // The section label keeps its own row above the two ways to add a source.
    const label = (await panel.getByText('Backing up', { exact: true }).boundingBox())!;
    const add = (await panel.getByRole('button', { name: 'Add folder', exact: true }).boundingBox())!;
    expect(add.y).toBeGreaterThanOrEqual(label.y + label.height - 1);
    // Nothing anywhere in the panel reaches past its own width.
    expect(await overflowsSideways(panel)).toBe(false);
});
