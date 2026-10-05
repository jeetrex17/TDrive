import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { expect, test } from '@playwright/test';
import { bootTDrive, rejects, resolves } from './wails-mock';

const source = { channel_id: 50, title: 'Field Recordings', username: 'fieldrec', connected: true, protected: false, available: true, account_id: 7, generation: 'source-a' };
const candidate = { ...source, channel_id: 60, title: 'Tech Talks', username: 'techtalks', connected: false, generation: '' };
const media = { msg_id: 71, date: 1_735_689_600, name: 'forest-dawn.mp4', size: 2048, duration: 754, mime_type: 'video/mp4', kind: 'video', caption: '', streamable: true, block_reason: '', telegram_url: 'https://t.me/fieldrec/71' };
const paid = { ...media, msg_id: 70, name: 'members-cut.mp4', streamable: false, block_reason: 'paid' };
const mediaPage = { channel_id: 50, account_id: 7, generation: 'source-a', items: [media, paid], next_offset_id: 0, has_more: false };

test('desktop channels sit beside the drives and keep the app header', async ({ page }) => {
    const mock = await bootTDrive(page, {
        ListConnectedChannelSources: resolves([source]),
        ListChannelSourceCandidates: resolves([source, candidate]),
        ListChannelMedia: resolves(mediaPage),
        ConnectChannelSource: resolves({ ...candidate, connected: true, generation: 'source-b' }),
    });
    const channel = page.locator('.sidebar').getByRole('button', { name: 'Field Recordings' });
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

    await page.getByRole('button', { name: 'Add a channel' }).click();
    const dialog = page.getByRole('dialog', { name: 'Add a channel' });
    await expect(dialog.getByRole('button', { name: 'Open Field Recordings, already added' })).toBeVisible();
    await dialog.getByRole('button', { name: 'Add Tech Talks' }).click();
    await expect(dialog).toBeHidden();
    expect((await mock.calls('ConnectChannelSource')).map((call) => call.args)).toEqual([[60, 7]]);

    await page.getByRole('button', { name: 'Personal', exact: true }).click();
    await expect(page.locator('#file-list')).toBeVisible();
    await expect(page.locator('.channel-view')).toHaveCount(0);
});

test('mobile channels open inside Files with their own way back', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await bootTDrive(page, {
        ListConnectedChannelSources: resolves([source]),
        ListChannelMedia: resolves(mediaPage),
    }, { url: '/?mobile=ios' });
    await page.getByRole('button', { name: /Switch drive/ }).click();
    await page.locator('.drive-switcher-sheet').getByRole('button', { name: 'Field Recordings' }).click();
    await expect(page.getByRole('heading', { name: 'Field Recordings' })).toBeVisible();
    await expect(page.getByRole('button', { name: /^Play forest-dawn/ })).toBeVisible();
    await expect(page.locator('.tab-bar')).toBeVisible();
    await expect(page.locator('.drive-switcher-sheet')).toHaveAttribute('inert', '');
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);

    await page.getByRole('button', { name: 'Back' }).click();
    await expect(page.locator('.channel-view')).toHaveCount(0);
    await expect(page.locator('#file-list')).toBeVisible();
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
    await page.locator('.sidebar').getByRole('button', { name: 'Field Recordings' }).click();
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
    expect(opened[0]).toEqual([50, 71, 7, 'source-a']);
    for (const args of opened.slice(1)) expect(args).toEqual([50, 69, 7, 'source-a']);
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
    await page.locator('.sidebar').getByRole('button', { name: 'Field Recordings' }).click();
    await page.getByRole('button', { name: /^Play Feature film/ }).click();

    await expect(page.locator('#video-error')).toBeVisible();
    await expect(page.locator('#video-error-retry')).toBeVisible();
    expect(await mock.calls('OpenNativeMedia')).toEqual([]);
});
