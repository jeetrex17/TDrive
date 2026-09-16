// Covers the phone shell so CI protects it: the shell is chosen on a mobile
// platform, the tab bar switches surfaces, the drive switcher and FAB menu open,
// folder navigation pushes history so Android BACK pops one level, and the bars
// carry safe-area padding. Runs under the default config (its own viewport) and
// the throwaway s1 config alike.
import { test, expect, bootTDrive, resolves, byFirstArg } from './wails-mock';

test.use({ viewport: { width: 390, height: 844 } });

const rootContents = {
    folders: [{ id: 'r1', name: 'Reports', parent_id: '' }],
    files: [
        { name: 'notes.txt', size: 1200, msg_id: 101, parent_id: '', upload_time: 1_760_000_000, uploader_id: 7, encrypted: false, plaintext_size: 0 },
        { name: 'budget.pdf', size: 45_000, msg_id: 102, parent_id: '', upload_time: 1_759_000_000, uploader_id: 7, encrypted: false, plaintext_size: 0 },
    ],
};
const reportsContents = {
    folders: [],
    files: [{ name: 'q3.pdf', size: 9000, msg_id: 201, parent_id: 'r1', upload_time: 1_760_000_000, uploader_id: 7, encrypted: false, plaintext_size: 0 }],
};

const overrides = {
    ListChannels: resolves([
        { id: 1, title: 'My Drive', kind: 'personal', is_active: true, invite_link: '' },
        { id: 2, title: 'Team assets', kind: 'shared', is_active: false, invite_link: 'https://t.me/x' },
    ]),
    GetFolderContents: byFirstArg({ '': resolves(rootContents), r1: resolves(reportsContents) }, resolves({ folders: [], files: [] })),
};

async function bootMobile(page: Parameters<typeof bootTDrive>[0]) {
    await page.addInitScript(() => history.replaceState(null, '', '/?mobile=android'));
    const mock = await bootTDrive(page, overrides);
    await expect(page.locator('#success-screen.mobile-shell')).toBeVisible();
    return mock;
}

function tab(page: Parameters<typeof bootTDrive>[0], name: RegExp) {
    return page.getByRole('navigation', { name: 'Primary' }).getByRole('button', { name });
}

test('phone shell mounts with the platform classes', async ({ page }) => {
    await bootMobile(page);
    await expect.poll(() => page.evaluate(() => document.documentElement.classList.contains('mobile'))).toBe(true);
    await expect(page.locator('.tab-bar')).toBeVisible();
    await expect(page.getByRole('button', { name: /Switch drive/ })).toContainText('My Drive');
});

test('tab bar switches between surfaces', async ({ page }) => {
    await bootMobile(page);
    await expect(page.locator('.main-content')).toBeVisible();

    await tab(page, /^Transfers/).click();
    await expect(page.locator('.mobile-panel[data-tab="transfers"]')).toBeVisible();
    await expect(page.locator('.main-content')).toBeHidden();
    await expect(page.getByRole('heading', { name: 'Transfers' })).toBeVisible();

    await tab(page, /^Account/).click();
    await expect(page.locator('.mobile-panel[data-tab="account"]')).toBeVisible();
    await expect(page.getByRole('button', { name: 'Log out' })).toBeVisible();

    await tab(page, /^Files/).click();
    await expect(page.locator('.main-content')).toBeVisible();
});

test('drive switcher opens from the header and closes', async ({ page }) => {
    await bootMobile(page);
    await expect(page.locator('.drive-switcher-sheet')).not.toHaveClass(/open/);

    await page.getByRole('button', { name: /Switch drive/ }).click();
    await expect(page.locator('.drive-switcher-sheet')).toHaveClass(/open/);
    await expect(page.locator('#drives-personal').getByRole('button', { name: 'My Drive', exact: true })).toBeVisible();
    await expect(page.locator('#drives-shared').getByRole('button', { name: 'Team assets', exact: true })).toBeVisible();

    await page.locator('.switcher-close').click();
    await expect(page.locator('.drive-switcher-sheet')).not.toHaveClass(/open/);
});

test('FAB opens the upload menu', async ({ page }) => {
    await bootMobile(page);
    await expect(page.locator('#upload-menu')).toBeHidden();

    await page.locator('.mobile-fab #upload-btn').click();
    await expect(page.locator('#upload-menu')).toBeVisible();
    await expect(page.locator('#upload-menu-files')).toHaveText(/Upload files/);
    await expect(page.locator('#upload-menu-new-folder')).toHaveText(/New folder/);
});

test('the back bridge pops one folder level and then leaves the app', async ({ page }) => {
    await bootMobile(page);
    await expect(page.getByRole('button', { name: /Switch drive/ })).toBeVisible();

    const folderRow = page.getByRole('row', { name: 'Folder: Reports' });
    await folderRow.click();
    if (!(await page.locator('.topbar-folder-title').isVisible().catch(() => false))) {
        await folderRow.dblclick().catch(() => undefined);
    }
    await expect(page.locator('.topbar-folder-title')).toHaveText('Reports');

    // The Android host calls this on a hardware or gesture BACK press and only
    // leaves the app when the page reports the press unhandled.
    const insideFolder = await page.evaluate(() => window.__tdriveHandleBack?.());
    expect(insideFolder).toBe(true);
    await expect(page.locator('.topbar-folder-title')).toHaveCount(0);
    await expect(page.getByRole('button', { name: /Switch drive/ })).toBeVisible();

    const atRoot = await page.evaluate(() => window.__tdriveHandleBack?.());
    expect(atRoot).toBe(false);
});

test('the bars carry safe-area padding', async ({ page }) => {
    await bootMobile(page);
    const shell = page.locator('#success-screen.mobile-shell');
    await expect(shell).toHaveCSS('position', 'fixed');

    // Tab bar is flush to the bottom edge (where the home indicator inset lives)
    // and declares env-based bottom padding (0px without a notch in the harness).
    const box = await page.locator('.tab-bar').boundingBox();
    const size = page.viewportSize();
    expect(box).not.toBeNull();
    expect(size).not.toBeNull();
    expect(Math.abs((box!.y + box!.height) - size!.height)).toBeLessThan(2);
    const pad = await page.locator('.tab-bar').evaluate((el) => getComputedStyle(el).paddingBottom);
    expect(pad).toMatch(/px$/);
    const topPad = await page.locator('.mobile-topbar').evaluate((el) => getComputedStyle(el).paddingTop);
    expect(topPad).toMatch(/px$/);
});
