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
    // Personal drives and shared drives are still two groups in the sheet, in
    // that order, the way the desktop sidebar splits them.
    const groups = page.locator('.drive-switcher-sheet .switcher-group');
    await expect(groups.nth(0).getByRole('button', { name: 'My Drive', exact: true })).toBeVisible();
    await expect(groups.nth(1).getByRole('button', { name: 'Team assets', exact: true })).toBeVisible();
    const list = page.locator('.drive-switcher-sheet .switcher-list');
    expect(await list.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
    const sharedRow = groups.nth(1).locator('.sidebar-drive-row');
    const action = sharedRow.getByRole('button', { name: 'Actions for Team assets' });
    const [rowBox, actionBox] = await Promise.all([sharedRow.boundingBox(), action.boundingBox()]);
    expect(rowBox).not.toBeNull();
    expect(actionBox).not.toBeNull();
    expect(actionBox!.x + actionBox!.width).toBeLessThanOrEqual(rowBox!.x + rowBox!.width + 1);

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

test('an empty folder offers file upload, folder upload, and folder creation', async ({ page }) => {
    await page.addInitScript(() => history.replaceState(null, '', '/?mobile=android'));
    await bootTDrive(page, {
        ...overrides,
        GetFolderContents: resolves({ folders: [], files: [] }),
        GetFileList: resolves([]),
    });
    await expect(page.locator('#success-screen.mobile-shell')).toBeVisible();

    const actions = page.locator('#file-list .file-state-actions');
    await expect(actions.getByRole('button')).toHaveText([
        'Upload files',
        'Upload folder',
        'Create folder',
    ]);
    await expect(page.locator('.mobile-context-action')).toBeHidden();
    expect(await actions.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
});

test('Android keyboard events keep the app shell fixed while the join sheet makes room', async ({ page }) => {
    const mock = await bootMobile(page);
    await page.getByRole('button', { name: /Switch drive/ }).click();
    await page.getByRole('button', { name: 'Join with a link' }).click();
    await expect(page.locator('#join-drive-link')).toBeFocused();

    const shell = page.locator('#success-screen.mobile-shell');
    const before = await shell.boundingBox();
    const scrollBefore = await page.evaluate(() => window.scrollY);
    const devicePixelRatio = await page.evaluate(() => window.devicePixelRatio);

    await mock.emit('common:keyboard', { visible: true, height: 300 * devicePixelRatio });
    await expect.poll(() => page.evaluate(() =>
        getComputedStyle(document.documentElement).getPropertyValue('--mobile-keyboard-inset').trim(),
    )).toBe('300px');

    expect(await shell.boundingBox()).toEqual(before);
    expect(await page.evaluate(() => window.scrollY)).toBe(scrollBefore);
    await expect(page.locator('#join-drive-link')).toBeVisible();
    await expect(page.locator('#join-drive-go')).toBeVisible();
});

test('the back bridge pops one folder level and then leaves the app', async ({ page }) => {
    await bootMobile(page);
    await expect(page.getByRole('button', { name: /Switch drive/ })).toBeVisible();

    const folderRow = page.getByRole('listitem', { name: 'Folder: Reports' });
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

    // The tab bar floats: its slot holds it --tabbar-inset clear of every edge,
    // and adds the home-indicator inset underneath (0px without a notch in the
    // harness). So the gap below it is the inset, not zero -- what matters is
    // that the slot reserves the safe area rather than letting the bar sit on
    // it. This asserted flush-to-the-bottom for as long as the bar has floated;
    // nothing caught it because CI only runs on a pull request, and this branch
    // had 123 commits before it opened one.
    const box = await page.locator('.tab-bar').boundingBox();
    const size = page.viewportSize();
    expect(box).not.toBeNull();
    expect(size).not.toBeNull();
    const gapBelow = size!.height - (box!.y + box!.height);
    const reserved = await page.locator('.mobile-tabbar-slot').evaluate((el) => {
        const style = getComputedStyle(el);
        return Number.parseFloat(style.paddingBottom);
    });
    expect(reserved).toBeGreaterThan(0);
    expect(Math.abs(gapBelow - reserved)).toBeLessThan(2);
    const pad = await page.locator('.mobile-tabbar-slot').evaluate((el) => getComputedStyle(el).paddingBottom);
    expect(pad).toMatch(/px$/);
    const topPad = await page.locator('.mobile-topbar').evaluate((el) => getComputedStyle(el).paddingTop);
    expect(topPad).toMatch(/px$/);
});

/**
 * Photos and Trash are each turned off from the refresh that turns the other
 * on. A setter that cleared the shared view unconditionally wiped out what its
 * sibling had just published: the Photos tab read as Files, the top bar kept
 * the drive header, and BACK at the gallery root left the app.
 */
test('the Photos tab lights, wears its own top bar, and BACK stays in the app', async ({ page }) => {
    await bootMobile(page);
    await tab(page, /^Photos/).click();
    await expect(page.locator('.main-content.photos-mode')).toBeVisible();
    await expect(tab(page, /^Photos/)).toHaveClass(/active/);
    await expect(page.locator('.topbar-files')).toBeHidden();
    await expect(page.locator('.topbar-plain').getByRole('heading', { name: 'Photos' })).toBeVisible();

    // Leaving Photos is one BACK, handled here, not by the host.
    const atPhotos = await page.evaluate(() => window.__tdriveHandleBack?.());
    expect(atPhotos).toBe(true);
    await expect(tab(page, /^Files/)).toHaveClass(/active/);
});
