import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { expect, test, type Locator } from '@playwright/test';
import { RED_BASE64 } from './gallery-fixtures';
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

async function expectMobileToastActionBelowCopy(toast: Locator): Promise<void> {
    const copy = await toast.locator('.toast-content').boundingBox();
    const action = await toast.getByRole('button', { name: 'Undo' }).boundingBox();
    const close = await toast.getByRole('button', { name: 'Dismiss' }).boundingBox();
    expect(copy).not.toBeNull();
    expect(action).not.toBeNull();
    expect(close).not.toBeNull();
    expect(action!.y).toBeGreaterThanOrEqual(copy!.y + copy!.height - 2);
    expect(action!.x).toBeGreaterThanOrEqual(copy!.x);
    expect(close!.x).toBeGreaterThan(copy!.x + copy!.width - 2);
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
    await expectMobileToastActionBelowCopy(toast);
    await toast.screenshot({ path: 'test-results/toast-action-mobile.png' });
    await page.setViewportSize({ width: 320, height: 640 });
    await expectMobileToastActionBelowCopy(toast);
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
    const thumbnails: string[] = [];
    page.on('request', (request) => { if (request.url().includes('forbidden-thumbnail')) thumbnails.push(request.url()); });
    await page.route('**/__fixtures__/tiny.mp4', (route) => route.fulfill({
        status: 200, contentType: 'video/mp4', headers: { 'Accept-Ranges': 'bytes' }, body: FIXTURE,
    }));
    const dawn = { ...media, caption: 'Dawn chorus', protected: true };
    const coast = { ...media, msg_id: 69, name: 'Telegram media 69.mp4', caption: 'Coastline at night', protected: true };
    const storm = { ...media, msg_id: 68, name: 'storm.mp3', kind: 'audio', mime_type: 'audio/mpeg' };
    const mock = await bootTDrive(page, {
        ListConnectedChannelSources: resolves([source]),
        ListChannelMedia: resolves({ ...mediaPage, items: [dawn, paid, coast, storm] }),
        OpenChannelMedia: resolves({ ...channelStream(71, dawn.name), info: { ...channelStream(71, dawn.name).info, protected: true }, thumbnail_url: 'http://127.0.0.1:4173/forbidden-thumbnail' }),
        CloseMedia: resolves(null),
        UpdateMediaPlayback: resolves(null),
    });
    await page.locator('.sidebar').getByRole('button', { name: 'Channel Field Recordings', exact: true }).click();
    await page.getByRole('button', { name: /^Play Dawn chorus/ }).click();

    await expect(page.locator('#video-shell')).toBeVisible();
    await expect(page.locator('#video-filename')).toHaveText('Dawn chorus');
    const video = page.locator('#video-player');
    await expect.poll(() => video.evaluate((element) => (element as HTMLVideoElement).readyState)).toBeGreaterThanOrEqual(2);
    await expect.poll(() => video.evaluate((element) => (element as HTMLVideoElement).videoWidth)).toBeGreaterThan(0);
    await expect(page.locator('#video-protected')).toBeVisible();
    await expect(video).toHaveAttribute('controlslist', 'nodownload');
    expect(await video.evaluate((element) => element.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true })))).toBe(false);
    expect(await video.evaluate((element) => element.dispatchEvent(new Event('copy', { bubbles: true, cancelable: true })))).toBe(false);
    expect(await mock.calls('Thumbnail')).toEqual([]);

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
    expect(thumbnails).toEqual([]);
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


// A synthetic one-page PDF with real cross-reference offsets for PDF.js.
function sourceGuidePdf(): Buffer {
    const content = 'BT /F1 24 Tf 40 120 Td (Source guide) Tj ET';
    const objects = [
        '<< /Type /Catalog /Pages 2 0 R >>',
        '<< /Type /Pages /Kids [3 0 R] /Count 1 >>',
        '<< /Type /Page /Parent 2 0 R /MediaBox [0 0 240 180] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>',
        `<< /Length ${content.length} >>\nstream\n${content}\nendstream`,
        '<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>',
    ];
    let pdf = '%PDF-1.4\n';
    const offsets = objects.map((object, index) => {
        const offset = Buffer.byteLength(pdf);
        pdf += `${index + 1} 0 obj\n${object}\nendobj\n`;
        return offset;
    });
    const xref = Buffer.byteLength(pdf);
    pdf += `xref\n0 6\n0000000000 65535 f \n${offsets.map((offset) => `${String(offset).padStart(10, '0')} 00000 n \n`).join('')}`;
    pdf += `trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n${xref}\n%%EOF\n`;
    return Buffer.from(pdf);
}

for (const platform of ['ios', 'android']) {
    test(`${platform} protected source videos stream with saving controls disabled`, async ({ page }) => {
        const thumbnails: string[] = [];
        page.on('request', (request) => { if (request.url().includes('forbidden-thumbnail')) thumbnails.push(request.url()); });
        await page.setViewportSize({ width: 390, height: 844 });
        await page.route('**/__fixtures__/tiny.mp4', (route) => route.fulfill({
            status: 200, contentType: 'video/mp4', headers: { 'Accept-Ranges': 'bytes' }, body: FIXTURE,
        }));
        const protectedVideo = { ...media, protected: true };
        const opened = channelStream(71, media.name);
        const mock = await bootTDrive(page, {
            ListConnectedChannelSources: resolves([source]),
            ListChannelMedia: resolves({ ...mediaPage, items: [protectedVideo] }),
            OpenChannelMedia: resolves({ ...opened, info: { ...opened.info, protected: true }, thumbnail_url: 'http://127.0.0.1:4173/forbidden-thumbnail' }),
            CloseMedia: resolves(null),
            UpdateMediaPlayback: resolves(null),
            ...(platform !== 'desktop' ? { SetScreenProtect: resolves(null) } : {}),
        }, { url: platform === 'desktop' ? '/' : `/?mobile=${platform}` });
        if (platform !== 'desktop') await page.getByRole('button', { name: /Switch drive/ }).click();
        await page.locator(platform === 'desktop' ? '.sidebar' : '.drive-switcher-sheet')
            .getByRole('button', { name: 'Channel Field Recordings', exact: true }).click();
        await page.getByRole('button', { name: /^Play forest-dawn/ }).click();
        const video = page.locator('#video-player');
        await expect.poll(() => video.evaluate((element) => (element as HTMLVideoElement).readyState)).toBeGreaterThanOrEqual(2);
        await expect(page.locator('#video-protected')).toBeVisible();
        await expect(video).toHaveAttribute('controlslist', 'nodownload');
        await expect(video).not.toHaveAttribute('poster', /.+/);
        expect(await video.evaluate((element) => element.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true })))).toBe(false);
        expect(await page.locator('#video-close').evaluate((element) => element.dispatchEvent(new KeyboardEvent('keydown', { key: 'p', ctrlKey: true, bubbles: true, cancelable: true })))).toBe(false);
        await page.locator('#video-close').click();
        await expect.poll(async () => (await mock.calls('CloseMedia')).length).toBe(1);
        if (platform !== 'desktop') {
            expect((await mock.calls('SetScreenProtect')).map((call) => call.args[0])).toEqual([true]);
            await page.locator('.channel-view').getByRole('button', { name: 'Back', exact: true }).click();
            await expect.poll(async () => (await mock.calls('SetScreenProtect')).map((call) => call.args[0])).toEqual([true, false]);
        }
        expect(await mock.calls('OpenStream')).toEqual([]);
        expect(await mock.calls('OpenMedia')).toEqual([]);
        expect(thumbnails).toEqual([]);
    });
}

for (const { platform, protectedContent } of ['desktop', 'ios', 'android'].flatMap((platform) => [false, true].map((protectedContent) => ({ platform, protectedContent })))) {
    test(`${platform} ${protectedContent ? 'protected' : 'ordinary'} sources load image, PDF and text bytes in read-only viewers`, async ({ page }) => {
        if (platform !== 'desktop') await page.setViewportSize({ width: 320, height: 640 });
        const image = { ...media, msg_id: 81, name: 'forest.svg', kind: 'image', mime_type: 'image/svg+xml', duration: 0, protected: protectedContent };
        const guide = { ...media, msg_id: 80, name: 'guide.pdf', kind: 'pdf', mime_type: 'application/pdf', duration: 0, protected: protectedContent };
        const notes = { ...media, msg_id: 79, name: 'notes.txt', kind: 'text', mime_type: 'text/plain', duration: 0, protected: protectedContent };
        const imageBytes = Buffer.from(RED_BASE64, 'base64');
        const pdfBytes = sourceGuidePdf();
        const baseUrl = 'http://127.0.0.1:4173/__fixtures__/source';
        await page.route('**/__fixtures__/source/**', (route) => {
            const path = new URL(route.request().url()).pathname;
            return route.fulfill({ status: 200, contentType: path.endsWith('.pdf') ? 'application/pdf' : path.endsWith('.svg') ? 'image/svg+xml' : 'text/plain',
                body: path.endsWith('.pdf') ? pdfBytes : path.endsWith('.svg') ? imageBytes : 'Read directly from the source.' });
        });
        const stream = (item: typeof image) => ({ ...channelStream(item.msg_id, item.name), kind: item.kind,
            url: `${baseUrl}/${item.name}`, mime_type: item.mime_type, info: { ...channelStream(item.msg_id, item.name).info, protected: protectedContent } });
        const mock = await bootTDrive(page, {
            ListConnectedChannelSources: resolves([{ ...source, protected: protectedContent && platform === 'android' }]),
            ListChannelMedia: resolves({ ...mediaPage, items: [image, guide, notes] }),
            OpenChannelMedia: resolves(stream(image)),
            ...(protectedContent && platform !== 'desktop' ? { SetScreenProtect: resolves(null) } : {}),
            CloseMedia: resolves(null),
        }, { url: platform === 'desktop' ? '/' : `/?mobile=${platform}` });
        if (platform === 'desktop') {
            await page.locator('.sidebar').getByRole('button', { name: 'Channel Field Recordings', exact: true }).click();
        } else {
            await page.getByRole('button', { name: /Switch drive/ }).click();
            await page.locator('.drive-switcher-sheet').getByRole('button', { name: 'Channel Field Recordings', exact: true }).click();
        }
        const channel = page.locator('.channel-view');
        const kinds = channel.getByRole('group', { name: 'Show', exact: true });
        await kinds.getByRole('button', { name: 'Documents', exact: true }).click();
        expect((await mock.calls('ListChannelMedia')).slice(-1)[0]?.args[5]).toBe('document');
        await kinds.getByRole('button', { name: 'Images', exact: true }).click();
        expect((await mock.calls('ListChannelMedia')).slice(-1)[0]?.args[5]).toBe('image');
        await kinds.getByRole('button', { name: 'All', exact: true }).click();
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true);

        const imagePost = channel.getByRole('button', { name: /^Open forest\.svg,/ });
        if (protectedContent) {
            expect(await imagePost.evaluate((element) => element.dispatchEvent(new Event('copy', { bubbles: true, cancelable: true })))).toBe(false);
            await expect(imagePost).toHaveCSS('user-select', 'none');
        }
        await imagePost.click();
        const viewer = page.locator('#viewer-modal');
        const photo = viewer.getByRole('img', { name: 'forest.svg' });
        await expect(photo).toBeVisible();
        await expect.poll(() => photo.evaluate((element) => (element as HTMLImageElement).naturalWidth)).toBe(2);
        await expect(viewer.getByRole('button', { name: 'Download', exact: true })).toHaveCount(0);
        await expect(viewer.getByRole('status')).toHaveCount(0);
        if (protectedContent) {
            await expect(viewer.getByText('Protected', { exact: true })).toBeVisible();
            await expect(photo).toHaveAttribute('draggable', 'false');
            expect(await photo.evaluate((element) => element.dispatchEvent(new MouseEvent('contextmenu', { bubbles: true, cancelable: true })))).toBe(false);
            expect(await photo.evaluate((element) => element.dispatchEvent(new Event('dragstart', { bubbles: true, cancelable: true })))).toBe(false);
            expect(await viewer.getByRole('button', { name: 'Close file' }).evaluate((element) => element.dispatchEvent(new KeyboardEvent('keydown', { key: 's', ctrlKey: true, bubbles: true, cancelable: true })))).toBe(false);
        }
        const photoBox = await photo.boundingBox();
        const stageBox = await viewer.locator('.source-image-viewer').boundingBox();
        expect(photoBox!.height).toBeLessThanOrEqual(stageBox!.height + 1);
        if (platform === 'desktop') {
            await photo.dblclick();
            await expect(photo).toHaveCSS('transform', /matrix\(2\.5/);
            await photo.dblclick();
            await expect(photo).toHaveCSS('transform', 'none');
        } else {
            await photo.evaluate((element) => {
                element.dispatchEvent(new PointerEvent('pointerdown', { pointerId: 1, pointerType: 'touch', clientX: 120, clientY: 200 }));
                element.dispatchEvent(new PointerEvent('pointerdown', { pointerId: 2, pointerType: 'touch', clientX: 160, clientY: 200 }));
                element.dispatchEvent(new PointerEvent('pointermove', { pointerId: 2, pointerType: 'touch', clientX: 200, clientY: 200 }));
                element.dispatchEvent(new PointerEvent('pointerup', { pointerId: 1, pointerType: 'touch', clientX: 120, clientY: 200 }));
                element.dispatchEvent(new PointerEvent('pointerup', { pointerId: 2, pointerType: 'touch', clientX: 200, clientY: 200 }));
            });
            await expect(photo).toHaveCSS('transform', /matrix\(2,/);
        }

        await page.screenshot({ path: `test-results/source-image-${platform}-${protectedContent ? 'protected' : 'ordinary'}.png`, animations: 'disabled' });
        await viewer.getByRole('button', { name: 'Close file' }).click();
        await expect.poll(async () => (await mock.calls('CloseMedia')).map((call) => call.args[0])).toContain('channel-81');
        if (protectedContent && platform !== 'desktop') expect((await mock.calls('SetScreenProtect')).slice(-1)[0]?.args[0]).toBe(true);

        await mock.setPlan('OpenChannelMedia', resolves(stream(guide)));
        await channel.getByRole('button', { name: /^Open guide\.pdf,/ }).click();
        const frame = viewer.frameLocator('iframe');
        if (protectedContent) {
            await expect(frame.locator('body')).toHaveClass('is-protected');
            await expect(frame.locator('.textLayer')).toHaveCount(0);
            await expect(frame.locator('.annotationLayer')).toHaveCount(0);
            for (const key of ['c', 's', 'p', 'a']) {
                expect(await frame.locator('body').evaluate((element, value) => element.dispatchEvent(new KeyboardEvent('keydown', { key: value, ctrlKey: true, bubbles: true, cancelable: true })), key)).toBe(false);
            }
            expect(await frame.locator('body').evaluate((element) => element.dispatchEvent(new Event('copy', { bubbles: true, cancelable: true })))).toBe(false);
        } else {
            await expect(frame.locator('.textLayer')).toContainText('Source guide');
        }
        await expect(frame.locator('.page canvas')).toBeVisible();
        await expect.poll(() => frame.locator('.page canvas').evaluate((element) => {
            const canvas = element as HTMLCanvasElement;
            const pixels = canvas.getContext('2d')!.getImageData(0, 0, canvas.width, canvas.height).data;
            return pixels.some((value, index) => index % 4 === 0 && value < 100 && pixels[index + 3] > 0);
        })).toBe(true);
        await viewer.getByRole('button', { name: 'Close file' }).click();
        await expect.poll(async () => (await mock.calls('CloseMedia')).map((call) => call.args[0])).toContain('channel-80');
        if (protectedContent && platform !== 'desktop') expect((await mock.calls('SetScreenProtect')).slice(-1)[0]?.args[0]).toBe(true);

        await mock.setPlan('OpenChannelMedia', resolves(stream(notes)));
        await channel.getByRole('button', { name: /^Open notes\.txt,/ }).click();
        await expect(viewer.locator('.text-viewer-body')).toHaveText('Read directly from the source.');
        if (protectedContent) {
            await expect(viewer.locator('.text-viewer-body')).toHaveCSS('user-select', 'none');
            expect(await viewer.locator('.text-viewer-body').evaluate((element) => element.dispatchEvent(new Event('copy', { bubbles: true, cancelable: true })))).toBe(false);
        }
        await viewer.getByRole('button', { name: 'Close file' }).click();
        expect((await mock.calls('OpenChannelMedia')).map((call) => call.args)).toEqual([
            ['channel', 50, 81, 7, 'source-a'], ['channel', 50, 80, 7, 'source-a'], ['channel', 50, 79, 7, 'source-a'],
        ]);
        if (protectedContent && platform !== 'desktop') {
            const protections = (await mock.calls('SetScreenProtect')).map((call) => call.args[0]);
            expect(protections[protections.length - 1]).toBe(true);
            await channel.getByRole('button', { name: 'Back', exact: true }).click();
            await expect.poll(async () => (await mock.calls('SetScreenProtect')).slice(-1)[0]?.args[0]).toBe(false);
        }
        expect(await mock.calls('OpenStream')).toEqual([]);
        expect(await mock.calls('OpenMedia')).toEqual([]);
    });
}
