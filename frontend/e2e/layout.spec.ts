/**
 * The layout's symmetry, as numbers. The desktop content panel sits the same
 * distance from the window on every side, and from the sidebar's highlighted
 * rows; inside it, the title, the column labels and the row icons start on one
 * gutter and the last controls end on the same gutter from the other edge. A
 * toolbar row is one control height. These are the rules a reader feels as
 * "even" without being able to name them, so they are checked exactly.
 */

import { bootTDrive, byFirstArg, expect, resolves, test } from './wails-mock';
import type { Locator, Page } from '@playwright/test';

const INSET = 8;
const GUTTER = 16;
const CONTROL = 32;

const T = 1_760_000_000;
const file = (msg_id: number, name: string, size: number) => ({
    name, size, msg_id, parent_id: '', upload_time: T, uploader_id: 7, encrypted: false, plaintext_size: 0,
});
const plans = {
    ListChannels: resolves([{ id: 1, title: 'My Drive', kind: 'personal', is_active: true, invite_link: '' }]),
    GetFolderContents: byFirstArg(
        { '': resolves({ folders: [{ id: 'a', name: 'Documents', parent_id: '' }], files: [file(101, 'Quarterly report.pdf', 2_400_000)] }) },
        resolves({ folders: [], files: [] }),
    ),
    ListTrash: resolves([]),
};

async function box(locator: Locator) {
    const value = await locator.first().boundingBox();
    if (!value) throw new Error('element is not laid out');
    return value;
}

/** Where an element's text starts: its box plus its own left padding. */
async function textLeft(locator: Locator): Promise<number> {
    const [rect, padding] = await Promise.all([
        box(locator),
        locator.first().evaluate((element) => Number.parseFloat(getComputedStyle(element).paddingLeft)),
    ]);
    return rect.x + padding;
}

/** Lets entrance animations finish, so nothing is measured mid-scale. */
async function settle(page: Page) {
    await page.waitForFunction(() => document.getAnimations().every((animation) => animation.playState !== 'running'));
}

/** Within a pixel: sub-pixel layout rounds differently per engine. */
function near(actual: number, expected: number, label?: string) {
    expect(Math.abs(actual - expected), label).toBeLessThanOrEqual(1);
}

async function bootDesktop(page: Page) {
    await bootTDrive(page, plans);
    await expect(page.getByRole('row', { name: /Quarterly report/ })).toBeVisible();
}

test('the content panel is inset evenly from the window and the sidebar', async ({ page }) => {
    await bootDesktop(page);
    const viewport = page.viewportSize()!;
    const panel = await box(page.locator('.main-content'));
    const activeRow = await box(page.locator('.drive-item.active'));

    expect(panel.y).toBe(INSET);
    expect(viewport.width - (panel.x + panel.width)).toBe(INSET);
    expect(viewport.height - (panel.y + panel.height)).toBe(INSET);
    expect(panel.x - (activeRow.x + activeRow.width)).toBe(INSET);
    // The sidebar's rows keep the same inset from the window's left edge.
    expect(activeRow.x).toBe(INSET);
});

test('the panel content starts and ends on one gutter', async ({ page }) => {
    await bootDesktop(page);
    const panel = await box(page.locator('.main-content'));
    const left = panel.x + 1 + GUTTER;
    const right = panel.x + panel.width - 1 - GUTTER;

    expect(await textLeft(page.locator('.breadcrumb-link').last())).toBeCloseTo(left, 0);
    expect(await textLeft(page.locator('.file-table-header .col-name .file-sort-button'))).toBeCloseTo(left, 0);
    expect((await box(page.locator('.file-row .folder-chip'))).x).toBeCloseTo(left, 0);

    const avatar = await box(page.locator('#profile-trigger'));
    expect(avatar.x + avatar.width).toBeCloseTo(right, 0);
    const actions = await box(page.locator('.file-row .row-actions'));
    expect(actions.x + actions.width).toBeCloseTo(right, 0);
});

test('size figures end under their column label', async ({ page }) => {
    await bootDesktop(page);
    const label = await box(page.locator('.file-table-header .col-size .file-sort-button span').first());
    const figure = await box(page.locator('.file-row:not(.folder-row) .row-meta[aria-colindex="3"]'));
    expect(figure.x + figure.width).toBeCloseTo(label.x + label.width, 0);
});

test('every top bar control is one height, centred on the bar', async ({ page }) => {
    await bootDesktop(page);
    const bar = await box(page.locator('.main-content > header'));
    // The bar's own bottom hairline is not part of the space its controls sit in.
    const centre = bar.y + (bar.height - 1) / 2;
    for (const selector of ['#search-input', '.notif-bell', '.upload-btn', '#profile-trigger']) {
        const control = await box(page.locator(selector));
        expect(control.height, selector).toBe(CONTROL);
        near(control.y + control.height / 2, centre, selector);
    }
});

test('the logo sits on the top bar centre line and the icon column', async ({ page }) => {
    await bootDesktop(page);
    const bar = await box(page.locator('.main-content > header'));
    const logo = await box(page.locator('.logo-mark'));
    const icon = await box(page.locator('.drive-item .icon'));
    expect(Math.abs(logo.y + logo.height / 2 - (bar.y + bar.height / 2))).toBeLessThanOrEqual(1);
    expect(logo.x).toBe(icon.x);
});

test('every menu row is one height', async ({ page }) => {
    await bootDesktop(page);
    await page.getByRole('row', { name: /Quarterly report/ }).click({ button: 'right' });
    const rows = page.locator('.context-menu-panel button');
    await expect(rows.first()).toBeVisible();
    await settle(page);
    for (const row of await rows.all()) near((await box(row)).height, CONTROL);
    await page.keyboard.press('Escape');

    await page.locator('#profile-trigger').click();
    const items = page.locator('.profile-menu-item:visible');
    await expect(items.first()).toBeVisible();
    await settle(page);
    for (const item of await items.all()) near((await box(item)).height, CONTROL);
});

test('the sign-in phone field and button share one height and one width', async ({ page }) => {
    await bootTDrive(page, { CheckLoginStatus: resolves(false), LoginPhoneNumber: resolves(null) });
    await expect(page.locator('#telegram-country')).toBeVisible();
    await settle(page);
    // The country code and the number sit inside one framed field.
    const field = await box(page.locator('.phone-entry'));
    const send = await box(page.locator('.auth-form .primary-btn'));
    near(send.height, field.height);
    near(field.x, send.x);
    near(field.x + field.width, send.x + send.width);
});

test.describe('phone', () => {
    test.use({ viewport: { width: 390, height: 844 } });

    test('rows, the add button and the tab bar keep the same side margins', async ({ page }) => {
        await page.addInitScript(() => history.replaceState(null, '', '/?mobile=android'));
        await bootTDrive(page, plans);
        await expect(page.locator('#success-screen.mobile-shell')).toBeVisible();
        const row = page.locator('#file-list .drive-row').first();
        await expect(row).toBeVisible();

        const icon = await box(row.locator('.folder-chip, .file-type-icon'));
        expect(icon.x).toBe(GUTTER);
        const fab = await box(page.locator('.mobile-fab'));
        expect(390 - (fab.x + fab.width)).toBe(GUTTER);
        const tabs = await box(page.locator('.tab-bar'));
        expect(tabs.x).toBe(0);
        expect(tabs.width).toBe(390);
        // The add button floats one gutter above the bar, never on it.
        expect(tabs.y - (fab.y + fab.height)).toBe(GUTTER);
    });
});
