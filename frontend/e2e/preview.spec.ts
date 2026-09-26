/**
 * The photo preview's keyboard and focus contract.
 *
 * What the preview *shows* is already covered in app.spec: it fetches a
 * screen-sized rendition over HTTP, and a slow arrival cannot overwrite a
 * faster one. What is not covered, and is the part that regresses silently, is
 * how it behaves for someone who is not using a mouse. A modal that swallows
 * focus, or drops it on the body when it closes, is unusable with a keyboard
 * and unnavigable with a screen reader -- and nothing about the screenshot
 * would look wrong.
 *
 * None of this is reachable from a unit test: it needs real focus, which needs
 * a real browser.
 */

import { bootTDrive, expect, test } from './wails-mock';
import {
    FIRST_PHOTO,
    SECOND_PHOTO,
    galleryPlans,
    previewImageContents,
    routeRenditions,
} from './gallery-fixtures';
import type { Page } from '@playwright/test';

async function openGallery(page: Page): Promise<void> {
    await page.getByRole('button', { name: 'Photos' }).click();
    // The cell only becomes clickable once its thumbnail lease resolves.
    await expect(page.getByRole('button', { name: FIRST_PHOTO.name }).locator('img'))
        .toHaveAttribute('src', /^blob:/);
}

test('opening takes focus, Escape closes, and the grid gets focus back', async ({ page }) => {
    await routeRenditions(page);
    await bootTDrive(page, galleryPlans([FIRST_PHOTO]));
    await openGallery(page);

    await page.getByRole('button', { name: FIRST_PHOTO.name }).click();
    await expect(page.getByRole('dialog', { name: FIRST_PHOTO.name })).toBeVisible();

    // Focus has to move into the dialog, or the next keypress goes to whatever
    // is behind it -- the grid, which would scroll under the overlay.
    await expect(page.locator('#preview-close')).toBeFocused();

    await page.keyboard.press('Escape');
    await expect(page.getByRole('dialog', { name: FIRST_PHOTO.name })).toBeHidden();

    // Focus comes back to the cell that opened it, not the grid and certainly
    // not the body: closing leaves the keyboard exactly where it was, so the
    // next arrow key carries on through the photos.
    await expect(page.getByRole('button', { name: FIRST_PHOTO.name })).toBeFocused();
});

test('focus stays inside the dialog while it is open', async ({ page }) => {
    await routeRenditions(page);
    await bootTDrive(page, galleryPlans([FIRST_PHOTO]));
    await openGallery(page);
    await page.getByRole('button', { name: FIRST_PHOTO.name }).click();
    await expect(page.getByRole('dialog', { name: FIRST_PHOTO.name })).toBeVisible();

    // Walk further than the dialog has stops, forwards and back. Anything that
    // escapes the trap lands outside #preview-shell, which is the assertion.
    const inside = () => page.evaluate(
        () => Boolean(document.activeElement?.closest('#preview-shell')),
    );
    for (let step = 0; step < 8; step += 1) {
        await page.keyboard.press('Tab');
        expect(await inside(), `focus escaped forwards on tab ${step + 1}`).toBe(true);
    }
    for (let step = 0; step < 8; step += 1) {
        await page.keyboard.press('Shift+Tab');
        expect(await inside(), `focus escaped backwards on tab ${step + 1}`).toBe(true);
    }
});

test('the arrow keys move between photos and the bytes follow', async ({ page }) => {
    await routeRenditions(page);
    await bootTDrive(page, galleryPlans([FIRST_PHOTO, SECOND_PHOTO]));
    await openGallery(page);
    await page.getByRole('button', { name: FIRST_PHOTO.name }).click();
    await expect(page.getByRole('dialog', { name: FIRST_PHOTO.name })).toBeVisible();
    // Red is the first photo's preview rendition; blue is the second's.
    await expect.poll(() => previewImageContents(page)).toContain('#e11d48');

    await page.keyboard.press('ArrowRight');
    await expect(page.locator('#preview-filename')).toHaveText(SECOND_PHOTO.name);
    // The title changing is cheap; the image actually being the other photo is
    // the thing worth asserting.
    await expect.poll(() => previewImageContents(page)).toContain('#2563eb');

    await page.keyboard.press('ArrowLeft');
    await expect(page.locator('#preview-filename')).toHaveText(FIRST_PHOTO.name);
    await expect.poll(() => previewImageContents(page)).toContain('#e11d48');
});

test.describe('Android photo details', () => {
    test.use({ viewport: { width: 390, height: 844 } });

    test('the info control opens details and an upward swipe opens them again', async ({ page }) => {
        await routeRenditions(page);
        await bootTDrive(page, galleryPlans([FIRST_PHOTO]), { url: '/?mobile=android' });
        await openGallery(page);
        await page.getByRole('button', { name: FIRST_PHOTO.name }).click();
        await expect(page.getByRole('dialog', { name: FIRST_PHOTO.name })).toBeVisible();

        const infoButton = page.getByRole('button', { name: 'Photo info' });
        await infoButton.click();
        await expect(page.locator('#preview-modal')).toHaveClass(/is-info-open/);
        await expect(infoButton).toHaveAttribute('aria-pressed', 'true');
        await expect(page.locator('#preview-info')).toHaveAttribute('aria-hidden', 'false');

        await infoButton.click();
        await expect(page.locator('#preview-modal')).not.toHaveClass(/is-info-open/);

        const stage = page.locator('#preview-stage');
        const pointer = { pointerId: 1, pointerType: 'touch', isPrimary: true, clientX: 195, bubbles: true, cancelable: true };
        await stage.dispatchEvent('pointerdown', { ...pointer, clientY: 650 });
        await stage.dispatchEvent('pointermove', { ...pointer, clientY: 490 });
        await stage.dispatchEvent('pointerup', { ...pointer, clientY: 490 });

        await expect(page.locator('#preview-modal')).toHaveClass(/is-info-open/);
        await expect(infoButton).toHaveAttribute('aria-pressed', 'true');
    });
});
