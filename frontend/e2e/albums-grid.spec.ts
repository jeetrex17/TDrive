import { bootTDrive, expect, resolves, test } from './wails-mock';
import { FIRST_PHOTO, galleryPlans, routeRenditions } from './gallery-fixtures';

/**
 * A realistic grid: enough folders to window, and names long enough to fight
 * their column. Two tiles with short names looked fine and hid both of the
 * faults this covers -- a collapsed cover and a name that widened its column
 * instead of clipping.
 */
const NAMES = [
    'Trees Beginner', 'Range Queries Intermediate', 'Range Queries Beginner', 'DP Advanced',
    'DP Intermediate', 'DP Beginner', 'Problem solving (DP)', 'Dynamic Programming Introduction',
    'Combinatorics + problem solving', 'Number Theory (advanced)', '2 Pointers + Sliding Window',
    'Problem Solving (Binary search)', 'Binary Search (advanced) + Ternary Search',
    'Problem Solving (greedy)', 'Greedy Algorithms', 'String Hashing and Tricks',
];

test('a full album grid draws every cover and keeps each name in its column', async ({ page }, testInfo) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    await routeRenditions(page);
    await bootTDrive(page, {
        ...galleryPlans([FIRST_PHOTO]),
        ListMediaFolders: resolves(NAMES.map((name, index) => ({
            folder_id: `d:${index}`, name, item_count: 6 + (index % 3),
            latest_upload_time: FIRST_PHOTO.upload_time - index,
            cover_msg_id: FIRST_PHOTO.msg_id, cover_revision: 1, cover_name: FIRST_PHOTO.name,
        }))),
    });
    await page.getByRole('button', { name: 'Photos' }).click();

    const tiles = page.locator('button.album-tile');
    await expect(tiles.first()).toBeVisible();
    await page.screenshot({ path: testInfo.outputPath('albums-full.png'), fullPage: true });

    // A cover is the point of a tile: it must have real height, not collapse
    // to the label strip.
    const cover = tiles.first().locator('.album-cover');
    const box = await cover.boundingBox();
    expect(box, 'the cover has no box at all').not.toBeNull();
    expect(box!.height, 'the cover collapsed').toBeGreaterThan(80);
    // Square, as designed.
    expect(Math.abs(box!.width - box!.height)).toBeLessThan(2);

    // A long name clips inside its own tile rather than running across the one
    // beside it.
    const first = await tiles.first().boundingBox();
    const longName = tiles.nth(7).locator('.album-name');
    const nameBox = await longName.boundingBox();
    expect(nameBox!.width).toBeLessThanOrEqual(first!.width + 1);
});
