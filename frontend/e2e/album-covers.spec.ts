import { bootTDrive, expect, resolves, test } from './wails-mock';
import { FIRST_PHOTO, galleryPlans, routeRenditions } from './gallery-fixtures';

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

/**
 * Folder names are one nowrap line, so a long one is also the tile's
 * min-content width. Nothing about it may reach the tile beside it -- on a
 * phone, where two columns leave a name barely a third of its length, least
 * of all.
 */
const LONG_NAMES = [
    'Combinatorics + problem solving', 'Number Theory (advanced)',
    'Binary Search (advanced) + Ternary Search', 'Problem Solving (greedy)',
    'Advanced Bit Manipulation + Greedy Algorithms', 'Greedy Algorithms',
];

for (const shape of [
    { name: 'a phone', width: 390, height: 844, phone: true },
    { name: 'a window', width: 1280, height: 800, phone: false },
]) {
    test(`a long folder name stays inside its own tile on ${shape.name}`, async ({ page }) => {
        await page.setViewportSize({ width: shape.width, height: shape.height });
        if (shape.phone) await page.addInitScript(() => history.replaceState(null, '', '/?mobile=android'));
        await routeRenditions(page);
        await bootTDrive(page, {
            ...galleryPlans([FIRST_PHOTO]),
            ListMediaFolders: resolves(LONG_NAMES.map((name, index) => ({
                folder_id: `d:${index}`, name, item_count: 6,
                latest_upload_time: FIRST_PHOTO.upload_time - index,
                cover_msg_id: FIRST_PHOTO.msg_id, cover_revision: 1, cover_name: FIRST_PHOTO.name,
            }))),
        });
        await (shape.phone
            ? page.getByRole('navigation', { name: 'Primary' }).getByRole('button', { name: /^Photos/ })
            : page.getByRole('button', { name: 'Photos' })).click();

        const tiles = page.locator('button.album-tile');
        await expect(tiles.first()).toBeVisible();
        const boxes = await tiles.evaluateAll((nodes) => nodes.map((node) => {
            const tile = node.getBoundingClientRect();
            const name = node.querySelector('.album-name')!.getBoundingClientRect();
            const cover = node.querySelector('.album-cover')!.getBoundingClientRect();
            return { tile, name, cover };
        }));
        for (const { tile, name, cover } of boxes) {
            expect(name.right, 'a name printed past its tile').toBeLessThanOrEqual(tile.right + 1);
            expect(tile.width, 'a tile grew to fit its name').toBeLessThanOrEqual(cover.width + 1);
        }
        // Two columns on a phone, so the second tile begins after the first ends.
        const first = boxes[0];
        const second = boxes[1];
        expect(second.tile.left).toBeGreaterThanOrEqual(first.tile.right);
    });
}
