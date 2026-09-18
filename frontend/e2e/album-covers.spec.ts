import { bootTDrive, expect, resolves, test } from './wails-mock';
import { FIRST_PHOTO, galleryPlans } from './gallery-fixtures';

/**
 * The album grid with nothing to draw in it: no rendition ever arrives, which
 * is what a cold drive, a locked vault and an offline launch all look like.
 * The grid is still a grid -- that is the whole claim here, because a cover
 * that works its own height out from a square ratio collapses to the label
 * strip the moment it has no image inside it.
 */
const NAMES = ['Pictures', 'images', 'Originals', 'videos', 'test_photos', 'photos'];

test('a grid with no covers yet is still a grid', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await page.route('**/mock-renditions/**', (route) => route.abort());
    await bootTDrive(page, {
        ...galleryPlans([FIRST_PHOTO]),
        ListMediaFolders: resolves(NAMES.map((name, index) => ({
            folder_id: `d:${index}`, name, item_count: 6 + index,
            latest_upload_time: FIRST_PHOTO.upload_time - index,
            // Half the folders are covered by a video, which has a frame of
            // its own, and half by a file with no thumbnail to address at all.
            cover_msg_id: FIRST_PHOTO.msg_id,
            cover_revision: index % 2 === 0 ? 1 : 0,
            cover_name: index % 2 === 0 ? 'clip.mp4' : FIRST_PHOTO.name,
        }))),
    });
    await page.getByRole('button', { name: 'Photos' }).click();

    const tiles = page.locator('button.album-tile');
    await expect(tiles.first()).toBeVisible();
    const covers = page.locator('.album-cover');
    await expect(covers).toHaveCount(NAMES.length);

    const boxes = await covers.evaluateAll((nodes) => nodes.map((node) => node.getBoundingClientRect()));
    for (const box of boxes) {
        expect(box.height, 'a cover collapsed to its label strip').toBeGreaterThan(80);
        expect(Math.abs(box.width - box.height), 'a cover stopped being square').toBeLessThan(2);
    }
    // Rows are the height the window measured them at, so the second row
    // begins below the first rather than on top of it.
    const rows = new Set(boxes.map((box) => Math.round(box.top)));
    expect(rows.size).toBeGreaterThan(1);

    // Nothing renderable means a folder glyph, never a broken image.
    await expect(page.locator('.album-cover-glyph')).toHaveCount(NAMES.length);
});
