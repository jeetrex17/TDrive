import { bootTDrive, deferred, expect, resolves, test } from './wails-mock';

const source = {
    peer_kind: 'channel',
    peer_id: 50,
    channel_id: 50,
    title: 'Field Recordings',
    username: 'fieldrec',
    connected: true,
    protected: false,
    available: true,
    account_id: 7,
    generation: 'source-a',
};

const longStem = 'Estuary_Field_Recordings_S02E08_Late_Summer_Coast_2160p_Multilingual_Commentary_Directors_Cut';

function copyPlans(names: readonly string[], plannedNames: readonly string[] = names) {
    return {
        ListConnectedChannelSources: resolves([source]),
        ListChannelMedia: resolves({
            ...source,
            items: names.map((name, index) => ({
                msg_id: 100 - index,
                date: 1_735_689_600 - index,
                name,
                size: 2048,
                duration: 754,
                mime_type: 'video/x-matroska',
                kind: 'video',
                caption: '',
                streamable: true,
                block_reason: '',
                telegram_url: '',
                copy_identity: `copy-${index}`,
            })),
            next_offset_id: 0,
            has_more: false,
        }),
        ListSourceCopyFolders: resolves({
            drive_id: 1,
            folders: [{ id: 'd:archive', name: 'Field archive', parent_id: '' }],
        }),
        PreviewSourceCopyNames: resolves(plannedNames),
    };
}

test('expanded copy selection keeps long names compact and explains renames', async ({ page }) => {
    const original = `${longStem}:Bonus.mkv`;
    const companion = 'Shoreline_Bonus_Interview.mkv';
    const plannedStem = `${longStem}_Bonus`;
    const plannedName = `${plannedStem}.mkv`;
    await bootTDrive(page, copyPlans([original, companion], [plannedName, companion]));
    await page.getByRole('button', { name: 'Channel Field Recordings', exact: true }).click();
    await page.getByText(original, { exact: true }).click();
    await page.getByText(companion, { exact: true }).click({ modifiers: ['Shift'] });
    await page.getByRole('button', { name: 'Save 2 files to My Drive', exact: true }).click();

    const dialog = page.getByRole('dialog', { name: 'Save to My Drive', exact: true });
    const save = dialog.getByRole('button', { name: 'Save here', exact: true });
    const toggle = dialog.getByText('2 files selected', { exact: true });
    const selectedFiles = dialog.getByRole('list', { name: 'Selected files', exact: true });
    await expect(save).toBeEnabled();
    await expect(save).toHaveAccessibleDescription(/adds a number to names that are already in use/);
    await expect(selectedFiles).toBeHidden();

    await toggle.click();
    const rows = selectedFiles.getByRole('listitem');
    await expect(rows).toHaveCount(2);
    const renamed = rows.filter({ has: page.getByTitle(plannedName, { exact: true }) });
    const name = renamed.getByText(plannedStem, { exact: true });
    await expect(name).toBeVisible();
    await expect(renamed.getByText('.mkv', { exact: true })).toBeInViewport({ ratio: 1 });
    await expect(renamed).toContainText(`Renamed from ${original}`);
    const nameLines = await name.evaluate((element) => {
        const range = document.createRange();
        range.selectNodeContents(element);
        return new Set(Array.from(range.getClientRects(), ({ top }) => top)).size;
    });
    expect(nameLines).toBe(1);
    await expect(dialog.getByText('Copies are saved without vault encryption.', { exact: false })).toBeVisible();
    await expect(save).toBeInViewport();

    await toggle.press('Enter');
    await expect(selectedFiles).toBeHidden();
});

test('the mobile copy sheet keeps its footer in view with a long selection', async ({ page }) => {
    await page.setViewportSize({ width: 320, height: 640 });
    const names = Array.from({ length: 12 }, (_, index) => `${index + 1}_${longStem}.mkv`);
    await bootTDrive(page, { ...copyPlans(names), Haptic: resolves(null) }, { url: '/?mobile=ios' });
    await page.getByRole('button', { name: /Switch drive/ }).click();
    await page.locator('.drive-switcher-sheet').getByRole('button', { name: 'Channel Field Recordings', exact: true }).click();
    await page.getByRole('button', { name: `Actions for ${names[0]}`, exact: true }).click();
    await page.getByRole('dialog', { name: names[0], exact: true }).getByRole('button', { name: 'Select', exact: true }).click();
    for (const name of names.slice(1)) await page.getByText(name, { exact: true }).click();
    await page.getByRole('button', { name: 'Save 12 files to My Drive', exact: true }).click();

    const dialog = page.getByRole('dialog', { name: 'Save to My Drive', exact: true });
    await dialog.getByText('12 files selected', { exact: true }).click();
    const selectedFiles = dialog.getByRole('list', { name: 'Selected files', exact: true });
    await expect(selectedFiles.getByRole('listitem')).toHaveCount(12);
    await expect(dialog.getByText('Copies are saved without vault encryption.', { exact: false })).toBeInViewport({ ratio: 1 });
    await expect(dialog.getByRole('button', { name: 'Save here', exact: true })).toBeInViewport({ ratio: 1 });
    await expect(dialog.getByRole('button', { name: 'Cancel', exact: true })).toBeInViewport({ ratio: 1 });
    expect(await dialog.evaluate((element) => element.scrollWidth <= element.clientWidth + 1)).toBe(true);

    // All rows must still be reachable when the expanded list exceeds its height.
    const lastName = selectedFiles.getByTitle(names[names.length - 1], { exact: true });
    await selectedFiles.hover();
    await page.mouse.wheel(0, 1000);
    await expect(lastName).toBeInViewport();
    await expect(dialog.getByRole('button', { name: 'Save here', exact: true })).toBeInViewport({ ratio: 1 });
});

test('keyboard focus follows the copy folder editor', async ({ page }) => {
    await bootTDrive(page, copyPlans(['recording.mkv']));
    await page.getByRole('button', { name: 'Channel Field Recordings', exact: true }).click();
    await page.getByRole('button', { name: /^Play recording/ }).press('Shift+F10');
    await page.getByRole('menuitem', { name: 'Save to My Drive…', exact: true }).press('Enter');

    const dialog = page.getByRole('dialog', { name: 'Save to My Drive', exact: true });
    const newFolder = dialog.getByRole('button', { name: 'New folder', exact: true });
    await newFolder.press('Enter');
    await expect(dialog.getByRole('textbox', { name: 'Folder name', exact: true })).toBeFocused();
    await dialog.getByRole('button', { name: 'Cancel', exact: true }).first().press('Enter');
    await expect(newFolder).toBeFocused();
});

test('reduced motion keeps the busy copy sheet reachable after a swipe', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await page.emulateMedia({ reducedMotion: 'reduce' });
    const saved = { id: 'copy-1', name: 'recording.mkv', parent_id: '', status: 'saved', file_id: 400, message: '' };
    const mock = await bootTDrive(page, {
        ...copyPlans(['recording.mkv']),
        Haptic: resolves(null),
        SaveSourceCopies: deferred('save', { results: [saved], not_started: '' }),
    }, { url: '/?mobile=ios' });
    await page.getByRole('button', { name: /Switch drive/ }).click();
    await page.locator('.drive-switcher-sheet').getByRole('button', { name: 'Channel Field Recordings', exact: true }).click();
    await page.getByRole('button', { name: 'Actions for recording.mkv', exact: true }).click();
    await page.getByRole('button', { name: 'Save to My Drive', exact: true }).click();

    const dialog = page.getByRole('dialog', { name: 'Save to My Drive', exact: true });
    await dialog.getByRole('button', { name: 'Save here', exact: true }).click();
    await expect.poll(async () => (await mock.calls('SaveSourceCopies')).length).toBe(1);
    await expect(dialog.getByRole('button', { name: 'Stop saving', exact: true })).toBeInViewport({ ratio: 1 });

    const header = await dialog.locator('.sheet-grab').boundingBox();
    if (!header) throw new Error('The copy sheet is missing its header.');
    const x = header.x + header.width / 2;
    const y = header.y + 12;
    await page.mouse.move(x, y);
    await page.mouse.down();
    await page.mouse.move(x, y + 170, { steps: 6 });
    await page.mouse.up();

    await mock.release('save');
    const done = dialog.getByRole('button', { name: 'Done', exact: true });
    await expect(done).toBeInViewport({ ratio: 1 });
    await done.click();
    await expect(dialog).toBeHidden();
});
