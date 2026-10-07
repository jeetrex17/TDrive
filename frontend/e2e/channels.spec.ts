import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { expect, test, type Locator } from '@playwright/test';
import { bootTDrive, rejects, resolves } from './wails-mock';

const source = { peer_kind: 'channel', peer_id: 50, channel_id: 50, title: 'Field Recordings', username: 'fieldrec', connected: true, protected: false, available: true, account_id: 7, generation: 'source-a' };
const candidate = { ...source, peer_id: 60, channel_id: 60, title: 'Tech Talks', username: 'techtalks', connected: false, generation: '' };
const media = { msg_id: 71, date: 1_735_689_600, name: 'forest-dawn.mp4', size: 2048, duration: 754, mime_type: 'video/mp4', kind: 'video', caption: '', streamable: true, block_reason: '', telegram_url: 'https://t.me/fieldrec/71' };
const paid = { ...media, msg_id: 70, name: 'members-cut.mp4', streamable: false, block_reason: 'paid' };
const mediaPage = { peer_kind: 'channel', peer_id: 50, channel_id: 50, account_id: 7, generation: 'source-a', items: [media, paid], next_offset_id: 0, has_more: false };

async function expectToastActionOnRight(toast: Locator): Promise<void> {
    const copy = await toast.locator('.toast-content').boundingBox();
    const action = await toast.getByRole('button', { name: 'Undo' }).boundingBox();
    const close = await toast.getByRole('button', { name: 'Dismiss' }).boundingBox();
    expect(copy).not.toBeNull();
    expect(action).not.toBeNull();
    expect(close).not.toBeNull();
    expect(action!.x).toBeGreaterThanOrEqual(copy!.x + copy!.width);
    expect(close!.x).toBeGreaterThanOrEqual(action!.x + action!.width);
}

test('desktop channels sit beside the drives and keep the app header', async ({ page }) => {
    await page.clock.install();
    const mock = await bootTDrive(page, {
        ListConnectedChannelSources: resolves([source]),
        ListChannelSourceCandidates: resolves([source, candidate]),
        ListChannelMedia: resolves(mediaPage),
        ConnectChannelSource: resolves({ ...candidate, connected: true, generation: 'source-b' }),
        DisconnectChannelSource: resolves(null),
    });
    const channel = page.locator('.sidebar').getByRole('button', { name: 'Channel Field Recordings', exact: true });
    await channel.click();
    await expect(page.getByRole('heading', { name: 'Field Recordings' })).toBeVisible();
    await expect(channel).toHaveAttribute('aria-current', 'page');
    await expect(page.getByRole('button', { name: 'Personal', exact: true })).not.toHaveAttribute('aria-current', 'page');
    // The notification bell and account menu stay; nothing can be uploaded into a channel.
    await expect(page.locator('.notif-bell')).toBeVisible();
    await expect(page.locator('.upload-menu-wrap')).toBeHidden();
    await expect(page.locator('#file-list')).toBeHidden();
    await expect(page.getByRole('button', { name: /^Play forest-dawn\.mp4, Video, 12:34/ })).toBeVisible();
    await expect(page.getByRole('button', { name: /^Open in Telegram: members-cut\.mp4.*Paid$/ })).toBeVisible();
    await expect(page.getByText('paid', { exact: true })).toHaveCount(0);

    await channel.click({ button: 'right' });
    await expect(page.getByRole('menuitem', { name: 'Remove from TDrive' })).toBeVisible();
    await page.keyboard.press('Escape');

    await page.locator('.channel-nav-add').click();
    const dialog = page.getByRole('dialog', { name: 'Add a source' });
    await expect(dialog.getByRole('button', { name: 'Open Field Recordings, already connected' })).toBeVisible();
    await dialog.getByRole('button', { name: 'Connect Tech Talks' }).click();
    await expect(dialog).toBeHidden();
    expect((await mock.calls('ConnectChannelSource')).map((call) => call.args)).toEqual([['channel', 60, '', 7]]);

    await page.getByRole('button', { name: 'Personal', exact: true }).click();
    await expect(page.locator('#file-list')).toBeVisible();
    await expect(page.locator('.channel-view')).toHaveCount(0);

    await channel.click({ button: 'right' });
    await page.getByRole('menuitem', { name: 'Remove from TDrive' }).click();
    const toast = page.locator('.toast').filter({ hasText: 'Removed Field Recordings' });
    await expect(toast.getByRole('button', { name: 'Undo' })).toBeVisible();
    await expect(channel).toHaveCount(0);
    expect(await mock.calls('DisconnectChannelSource')).toHaveLength(0);
    await toast.getByRole('button', { name: 'Undo' }).click();
    await expect(channel).toBeVisible();
    await expect(toast).toHaveCount(0);
    expect(await mock.calls('DisconnectChannelSource')).toHaveLength(0);
    expect(await mock.calls('ConnectChannelSource')).toHaveLength(1);

    await channel.click({ button: 'right' });
    await page.getByRole('menuitem', { name: 'Remove from TDrive' }).click();
    await expect(toast.getByRole('button', { name: 'Undo' })).toBeVisible();
    await expectToastActionOnRight(toast);
    await toast.screenshot({ path: 'test-results/toast-action-desktop.png' });
    await page.clock.fastForward(8_001);
    await expect(toast).toHaveCount(0);
    await expect.poll(async () => (await mock.calls('DisconnectChannelSource')).length).toBe(1);
});

test('mobile channels open inside Files with their own way back', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    const mock = await bootTDrive(page, {
        ListConnectedChannelSources: resolves([source]),
        ListChannelMedia: resolves(mediaPage),
        DisconnectChannelSource: resolves(null),
    }, { url: '/?mobile=ios' });
    await page.getByRole('button', { name: /Switch drive/ }).click();
    await page.locator('.drive-switcher-sheet').getByRole('button', { name: 'Channel Field Recordings', exact: true }).click();
    await expect(page.getByRole('heading', { name: 'Field Recordings' })).toBeVisible();
    await expect(page.getByRole('button', { name: /^Play forest-dawn/ })).toBeVisible();
    await expect(page.locator('.tab-bar')).toBeVisible();
    await expect(page.locator('.drive-switcher-sheet')).toHaveAttribute('inert', '');
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);

    // A post's actions have a visible "…" with a 44px target, not only a long press.
    const rowActions = page.getByRole('button', { name: /^Actions for forest-dawn/ });
    await expect(rowActions).toBeVisible();
    const rowActionsBox = await rowActions.boundingBox();
    expect(rowActionsBox!.width).toBeGreaterThanOrEqual(44);
    expect(rowActionsBox!.height).toBeGreaterThanOrEqual(44);

    await page.getByRole('button', { name: 'Back' }).click();
    await expect(page.locator('.channel-view')).toHaveCount(0);
    await expect(page.locator('#file-list')).toBeVisible();

    await page.getByRole('button', { name: /Switch drive/ }).click();
    await page.locator('.drive-switcher-sheet').getByRole('button', { name: 'Channel Field Recordings', exact: true }).click();
    await page.getByRole('button', { name: 'Source actions' }).click();
    await page.getByRole('dialog', { name: 'Actions' }).getByRole('button', { name: 'Remove from TDrive' }).click();
    const toast = page.locator('.toast').filter({ hasText: 'Removed Field Recordings' });
    await expect(toast.getByRole('button', { name: 'Undo' })).toBeVisible();
    await expectToastActionOnRight(toast);
    await toast.screenshot({ path: 'test-results/toast-action-mobile.png' });
    await page.setViewportSize({ width: 320, height: 640 });
    await expectToastActionOnRight(toast);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
    await toast.screenshot({ path: 'test-results/toast-action-mobile-320.png' });
    expect(await mock.calls('DisconnectChannelSource')).toHaveLength(0);
    await toast.getByRole('button', { name: 'Undo' }).click();
    await expect(toast).toHaveCount(0);
    await expect(page.getByRole('heading', { name: 'Field Recordings' })).toBeVisible();
    expect(await mock.calls('DisconnectChannelSource')).toHaveLength(0);

    await mock.setPlan('DisconnectChannelSource', rejects('temporary failure'));
    await page.getByRole('button', { name: 'Source actions' }).click();
    await page.getByRole('dialog', { name: 'Actions' }).getByRole('button', { name: 'Remove from TDrive' }).click();
    await toast.getByRole('button', { name: 'Dismiss' }).click();
    await expect(page.getByRole('button', { name: /Switch drive/ })).toBeVisible();
    await page.getByRole('button', { name: /Switch drive/ }).click();
    await expect(page.locator('.drive-switcher-sheet').getByRole('button', { name: 'Channel Field Recordings', exact: true })).toBeVisible();
});

test('the picker distinguishes joined chat types and verifies a public link before it connects', async ({ page }) => {
    const direct = { peer_kind: 'user', peer_id: 50, channel_id: 0, title: 'Mina', username: '', connected: false, protected: false, available: true, account_id: 7, generation: '' };
    const publicChannel = { peer_kind: 'channel', peer_id: 89, channel_id: 89, title: 'Public Radio', username: 'publicradio', connected: false, protected: false, available: true, account_id: 7, generation: '' };
    const mock = await bootTDrive(page, {
        ListConnectedChannelSources: resolves([]),
        ListChannelSourceCandidates: resolves([direct]),
        ResolvePublicChannelSource: resolves(publicChannel),
        ConnectChannelSource: resolves({ ...publicChannel, connected: true, generation: 'public-a' }),
    });

    await page.locator('.channel-nav-add').click();
    const dialog = page.getByRole('dialog', { name: 'Add a source' });
    await expect(dialog.getByRole('button', { name: 'Connect Mina' })).toBeVisible();
    await expect(dialog.getByText('Direct message', { exact: true })).toBeVisible();
    await dialog.getByLabel('Public channel link').fill('https://t.me/publicradio');
    await dialog.getByRole('button', { name: 'Check link' }).click();
    await expect(dialog.getByRole('button', { name: /Public Radio/ })).toBeVisible();
    await dialog.getByRole('button', { name: /Public Radio/ }).click();
    expect((await mock.calls('ConnectChannelSource')).map((call) => call.args)).toEqual([['channel', 89, 'publicradio', 7]]);
});

test('desktop source picker has no horizontal overflow', async ({ page }) => {
    const direct = { peer_kind: 'user', peer_id: 50, channel_id: 0, title: 'Mina', username: '', connected: false, protected: false, available: true, account_id: 7, generation: '' };
    await bootTDrive(page, { ListChannelSourceCandidates: resolves([direct]) });
    await page.locator('.channel-nav-add').click();
    await page.screenshot({ path: 'test-results/source-picker-desktop.png', fullPage: true, animations: 'disabled' });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
});

test('390x844 mobile source picker has no sheet bleed', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    const direct = { peer_kind: 'user', peer_id: 50, channel_id: 0, title: 'Mina', username: '', connected: false, protected: false, available: true, account_id: 7, generation: '' };
    await bootTDrive(page, {
        ListChannelSourceCandidates: resolves([direct]),
        ResolvePublicChannelSource: resolves(candidate),
    }, { url: '/?mobile=ios' });
    await page.getByRole('button', { name: /Switch drive/ }).click();
    await page.locator('.channel-nav-add').click();
    const dialog = page.getByRole('dialog', { name: 'Add a source' });
    const link = dialog.getByLabel('Public channel link');
    expect(await link.evaluate((input) => parseFloat(getComputedStyle(input).fontSize))).toBeGreaterThanOrEqual(16);
    await link.fill('@techtalks');
    await dialog.getByRole('button', { name: 'Check link' }).click();
    const result = dialog.getByRole('button', { name: 'Connect Tech Talks' });
    await expect(result).toBeVisible();
    expect((await result.boundingBox())!.height).toBeGreaterThanOrEqual(60);
    await page.screenshot({ path: 'test-results/source-picker-mobile-390x844.png', fullPage: true, animations: 'disabled' });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);
});

test('the phone drive sheet gives channel rows a visible actions button', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await bootTDrive(page, {
        ListConnectedChannelSources: resolves([source]),
        ListChannelMedia: resolves(mediaPage),
    }, { url: '/?mobile=ios' });
    await page.getByRole('button', { name: /Switch drive/ }).click();

    const row = page.locator('.drive-switcher-sheet .channel-nav-row');
    const actions = row.getByRole('button', { name: 'Actions for Field Recordings' });
    await expect(actions).toBeVisible();
    const box = await actions.boundingBox();
    expect(box!.width).toBeGreaterThanOrEqual(44);
    expect(box!.height).toBeGreaterThanOrEqual(44);
});

test('picking a drive from the phone Account tab leaves the channel', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await bootTDrive(page, {
        ListConnectedChannelSources: resolves([source]),
        ListChannelMedia: resolves(mediaPage),
        SetActiveChannel: resolves(null),
    }, { url: '/?mobile=ios' });
    await page.getByRole('button', { name: /Switch drive/ }).click();
    await page.locator('.drive-switcher-sheet').getByRole('button', { name: 'Channel Field Recordings', exact: true }).click();
    await expect(page.getByRole('heading', { name: 'Field Recordings' })).toBeVisible();

    await page.locator('.tab-bar').getByRole('button', { name: /^Account/ }).click();
    await page.getByRole('region', { name: 'Drives' }).getByRole('button', { name: /^Personal/ }).click();
    await expect(page.locator('#file-list')).toBeVisible();
    await expect(page.locator('.channel-view')).toHaveCount(0);
});

const FIXTURE = readFileSync(join(__dirname, 'fixtures', 'tiny.mp4'));
const FIXTURE_URL = 'http://127.0.0.1:4173/__fixtures__/tiny.mp4';

/** A channel post's stream: the backend answers OpenChannelMedia the way it answers OpenMedia. */
function channelStream(msgId: number, name: string) {
    return {
        token: `channel-${msgId}`, url: FIXTURE_URL, thumbnail_url: '', hls_url: '', name, kind: 'video',
        mime_type: 'video/mp4', supports_range: true,
        info: { channel_id: 50, file_id: msgId, name, stored_size: FIXTURE.byteLength, plaintext_size: FIXTURE.byteLength },
    };
}

test("a channel video queues the channel's other videos in the player's playlist", async ({ page }) => {
    await page.route('**/__fixtures__/tiny.mp4', (route) => route.fulfill({
        status: 200, contentType: 'video/mp4', headers: { 'Accept-Ranges': 'bytes' }, body: FIXTURE,
    }));
    const dawn = { ...media, caption: 'Dawn chorus' };
    const coast = { ...media, msg_id: 69, name: 'Telegram media 69.mp4', caption: 'Coastline at night' };
    const storm = { ...media, msg_id: 68, name: 'storm.mp3', kind: 'audio', mime_type: 'audio/mpeg' };
    const mock = await bootTDrive(page, {
        ListConnectedChannelSources: resolves([source]),
        ListChannelMedia: resolves({ ...mediaPage, items: [dawn, paid, coast, storm] }),
        OpenChannelMedia: resolves(channelStream(71, dawn.name)),
        CloseMedia: resolves(null),
        UpdateMediaPlayback: resolves(null),
    });
    await page.locator('.sidebar').getByRole('button', { name: 'Channel Field Recordings', exact: true }).click();
    await page.getByRole('button', { name: /^Play Dawn chorus/ }).click();

    await expect(page.locator('#video-shell')).toBeVisible();
    await expect(page.locator('#video-filename')).toHaveText('Dawn chorus');
    const playlist = page.getByRole('button', { name: 'Playlist, 1 of 2' });
    await playlist.click();
    await expect(page.locator('#video-playlist-panel')).toContainText('Videos in Field Recordings');
    await expect(page.locator('#video-playlist-panel')).toContainText('Coastline at night');
    // Every video opens through its channel, never as a drive file. The tiny
    // fixture is within the prefetch lead at once, so the player may already
    // be warming the next one.
    const opened = (await mock.calls('OpenChannelMedia')).map((call) => call.args);
    expect(opened[0]).toEqual(['channel', 50, 71, 7, 'source-a']);
    for (const args of opened.slice(1)) expect(args).toEqual(['channel', 50, 69, 7, 'source-a']);
    expect(await mock.calls('OpenMedia')).toEqual([]);
});

test('a channel video bound for the native player reports a failed open', async ({ page }) => {
    const film = { ...media, msg_id: 72, name: 'feature.mkv', mime_type: 'video/x-matroska', caption: 'Feature film' };
    const mock = await bootTDrive(page, {
        ListConnectedChannelSources: resolves([source]),
        ListChannelMedia: resolves({ ...mediaPage, items: [film] }),
        OpenChannelMedia: rejects('rpc error code 420: FLOOD_WAIT_30'),
        CloseMedia: resolves(null),
    });
    await page.locator('.sidebar').getByRole('button', { name: 'Channel Field Recordings', exact: true }).click();
    await page.getByRole('button', { name: /^Play Feature film/ }).click();

    await expect(page.locator('#video-error')).toBeVisible();
    await expect(page.locator('#video-error-retry')).toBeVisible();
    expect(await mock.calls('OpenNativeMedia')).toEqual([]);
});


for (const platform of ['ios', 'android']) {
    test(`${platform} chat sources keep mobile actions and their place across tabs`, async ({ page }) => {
        await page.setViewportSize({ width: 390, height: 844 });
        const chat = { ...source, peer_kind: 'user', channel_id: 0, title: 'Mina', username: '' };
        const mock = await bootTDrive(page, {
            ListConnectedChannelSources: resolves([chat, source]),
            ListChannelMedia: resolves({ ...mediaPage, peer_kind: 'user', items: [media, { ...media, msg_id: 72, name: 'expired.mp4', streamable: false, block_reason: 'expired', telegram_url: '' }] }),
            DisconnectChannelSource: resolves(null),
        }, { url: `/?mobile=${platform}` });
        await page.getByRole('button', { name: /Switch drive/ }).click();
        const sheet = page.locator('.drive-switcher-sheet');
        const actions = sheet.getByRole('button', { name: 'Actions for Mina', exact: true });
        await expect(actions).toBeVisible();
        const box = await actions.boundingBox();
        expect(box!.width).toBeGreaterThanOrEqual(44);
        expect(box!.height).toBeGreaterThanOrEqual(44);
        await sheet.getByRole('button', { name: 'Direct message Mina', exact: true }).click();
        await expect(page.getByRole('heading', { name: 'Mina', exact: true })).toBeVisible();
        await page.locator('.tab-bar').getByRole('button', { name: /^Transfers/ }).click();
        await page.locator('.tab-bar').getByRole('button', { name: /^Files/ }).click();
        await expect(page.getByRole('heading', { name: 'Mina', exact: true })).toBeVisible();
        await expect(page.getByRole('button', { name: /^Actions for forest-dawn/ })).toBeVisible();
        await expect(page.getByRole('button', { name: /^Actions for expired/ })).toHaveCount(0);
        await page.getByRole('button', { name: 'Source actions' }).click();
        await page.getByRole('dialog', { name: 'Actions' }).getByRole('button', { name: 'Remove from TDrive' }).click();
        const toast = page.locator('.toast').filter({ hasText: 'Removed Mina' });
        await toast.getByRole('button', { name: 'Undo' }).click();
        await expect(page.getByRole('heading', { name: 'Mina', exact: true })).toBeVisible();
        expect(await mock.calls('DisconnectChannelSource')).toHaveLength(0);
    });
}
