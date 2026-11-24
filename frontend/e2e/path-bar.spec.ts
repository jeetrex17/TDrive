import { bootTDrive, byFirstArg, expect, resolves, test } from './wails-mock';

const plan = { name: 'Plan.pdf', size: 1, msg_id: 41, parent_id: 'q4', upload_time: 1, uploader_id: 7, encrypted: false, plaintext_size: 0 };

function reportsDrive() {
    return {
        GetFolderContents: byFirstArg({
            '': resolves({ folders: [{ id: 'reports', name: 'Reports', parent_id: '' }], files: [] }),
            reports: resolves({ folders: [{ id: 'q4', name: 'Q4', parent_id: 'reports' }], files: [] }),
            q4: resolves({ folders: [], files: [plan] }),
        }, resolves({ folders: [], files: [] })),
    };
}

test('opens a typed folder path from the breadcrumb with Ctrl/Cmd+L', async ({ page }) => {
    await bootTDrive(page, reportsDrive());
    const location = page.locator('#breadcrumb-path [aria-current="location"]');
    await expect(page.getByRole('row', { name: 'Folder: Reports' })).toBeVisible();

    const apple = await page.evaluate(() => /Mac|iPhone|iPad|iPod/i.test(navigator.platform));
    await page.keyboard.press(apple ? 'Meta+l' : 'Control+l');
    const field = page.getByRole('textbox', { name: 'Folder path' });
    await expect(field).toBeFocused();
    await expect(field).toHaveValue('/');

    await field.fill('/reports/q4/');
    await field.press('Enter');
    await expect(field).toHaveCount(0);
    await expect(location).toHaveText('Q4');
    await expect(page.getByRole('row', { name: 'File: Plan.pdf' })).toBeVisible();

    // Escape puts the trail back without going anywhere.
    await page.keyboard.press(apple ? 'Meta+l' : 'Control+l');
    await expect(field).toHaveValue('/Reports/Q4');
    await field.press('Escape');
    await expect(field).toHaveCount(0);
    await expect(location).toHaveText('Q4');
});

test('explains a missing path, then selects the file a pasted path names', async ({ page }) => {
    await bootTDrive(page, reportsDrive());
    await expect(page.getByRole('row', { name: 'Folder: Reports' })).toBeVisible();

    // The space beside the crumbs opens the field, as in File Explorer.
    const trail = page.locator('#breadcrumb-path');
    await trail.hover();
    const nav = (await trail.boundingBox())!;
    const crumb = (await trail.locator('.breadcrumb-link').last().boundingBox())!;
    const gapStart = crumb.x + crumb.width;
    expect(nav.x + nav.width - gapStart).toBeGreaterThan(10);
    await page.mouse.click((gapStart + nav.x + nav.width) / 2, nav.y + nav.height / 2);
    const field = page.getByRole('textbox', { name: 'Folder path' });
    await expect(field).toBeFocused();

    await field.fill('/Reports/Q5');
    await field.press('Enter');
    const message = page.locator('#breadcrumb-path-error');
    await expect(message).toHaveText('"Q5" was not found in /Reports.');
    await expect(field).toHaveAttribute('aria-invalid', 'true');
    await expect(field).toHaveValue('/Reports/Q5');

    await field.fill('Personal / Reports / Q4 / plan.pdf');
    await expect(message).toHaveCount(0);
    await field.press('Enter');
    await expect(page.locator('#breadcrumb-path [aria-current="location"]')).toHaveText('Q4');
    await expect(page.locator('[data-row-key="file:41"]')).toBeFocused();
});
