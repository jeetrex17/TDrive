/**
 * The video path, driven end to end against a real <video> element.
 *
 * This is the least covered code in the app and the most expensive to get
 * wrong: the controller coordinates a backend session token, an adapter that
 * may be the webview or an out-of-process native player, and a fallback between
 * them. None of it is reachable from a unit test, because it needs an element
 * that actually loads bytes.
 *
 * It does not need Telegram, though. The player takes its source from whatever
 * OpenMedia resolved with (see player-adapters playbackSource), so a mocked
 * open plus a routed fixture gives the real element a real file, and everything
 * downstream -- the transport, the chrome, the track pickers, the failure
 * branches -- runs as it does in the app.
 *
 * Both engines matter here and for different reasons. Chromium is the desktop
 * webview; WebKit is what iOS actually runs, and the iOS-only branch below
 * decides a source format that no Chromium run would exercise.
 */

import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { bootTDrive, expect, rejects, resolves, test } from './wails-mock';
import type { Page } from '@playwright/test';

/** Two seconds of navy with a 440Hz tone: small enough to commit, real enough to decode. */
const FIXTURE = readFileSync(join(__dirname, 'fixtures', 'tiny.mp4'));
const FIXTURE_URL = 'http://127.0.0.1:4173/__fixtures__/tiny.mp4';
/** Never fetched: the iOS test asserts which source is chosen, not that it decodes. */
const HLS_URL = 'http://127.0.0.1:4173/__fixtures__/tiny.m3u8';

const VIDEO_FILE = {
    name: 'clip.mp4',
    size: FIXTURE.byteLength,
    msg_id: 501,
    parent_id: '',
    upload_time: 1_735_689_600,
    uploader_id: 7,
    encrypted: false,
    plaintext_size: 0,
};

/** What the backend answers with when a video opens. Field names are the Go side's. */
function opened(overrides: Record<string, unknown> = {}) {
    return {
        token: 'test-token',
        url: FIXTURE_URL,
        thumbnail_url: '',
        hls_url: '',
        name: VIDEO_FILE.name,
        kind: 'video',
        mime_type: 'video/mp4',
        supports_range: true,
        info: {
            channel_id: 1,
            msg_id: VIDEO_FILE.msg_id,
            name: VIDEO_FILE.name,
            size: VIDEO_FILE.size,
        },
        ...overrides,
    };
}

/** Serves the fixture with byte ranges, as the real streaming endpoint does. */
async function serveFixture(page: Page): Promise<void> {
    await page.route('**/__fixtures__/tiny.mp4', (route) => {
        const range = route.request().headers()['range'];
        const match = /^bytes=(\d+)-(\d*)$/.exec(range ?? '');
        if (!match) return route.fulfill({
            status: 200,
            contentType: 'video/mp4',
            headers: { 'Accept-Ranges': 'bytes' },
            body: FIXTURE,
        });

        const start = Number(match[1]);
        const end = match[2] ? Number(match[2]) : FIXTURE.byteLength - 1;
        if (start >= FIXTURE.byteLength || end < start || end >= FIXTURE.byteLength) {
            return route.fulfill({ status: 416, headers: { 'Content-Range': `bytes */${FIXTURE.byteLength}` } });
        }
        return route.fulfill({
            status: 206,
            contentType: 'video/mp4',
            headers: {
                'Accept-Ranges': 'bytes',
                'Content-Range': `bytes ${start}-${end}/${FIXTURE.byteLength}`,
            },
            body: FIXTURE.subarray(start, end + 1),
        });
    });
}

/**
 * Opens it the way the app offers it: the row's own Play control. A
 * double-click selects the row and nothing more -- opening is a deliberate
 * action here, not a side effect of pointing at something.
 */
async function openTheVideo(page: Page): Promise<void> {
    // The two shells offer it differently: the desktop grid puts an explicit
    // Play control in the row's actions (a double-click there only selects),
    // while the phone list opens on tap. Ask for whichever this build renders.
    const desktopRow = page.getByRole('row', { name: `File: ${VIDEO_FILE.name}` });
    const mobileRow = page.getByRole('listitem', { name: `File: ${VIDEO_FILE.name}` });

    await expect(mobileRow.or(desktopRow)).toBeVisible();
    const phone = await mobileRow.count() > 0;

    // The phone list is virtualized: it can recycle the row's node between the
    // tap being dispatched and the handler running, which drops the tap. Open
    // against the outcome rather than the gesture, so a swallowed tap is
    // retried instead of failing the run.
    await expect(async () => {
        if (phone) await mobileRow.click();
        else await desktopRow.getByRole('button', { name: 'Play video' }).click();
        await expect(page.locator('#video-shell')).toBeVisible({ timeout: 2_000 });
    }).toPass({ timeout: 15_000 });
}

test('plays a real file, and closing it releases the backend session', async ({ page }) => {
    await serveFixture(page);
    const mock = await bootTDrive(page, {
        GetFolderContents: resolves({ folders: [], files: [VIDEO_FILE] }),
        OpenMedia: resolves(opened()),
    });

    await openTheVideo(page);

    const player = page.locator('#video-player');
    await expect(player).toBeVisible();
    // Source arrives from the open result, not from anything the page invents.
    await expect(player).toHaveAttribute('src', FIXTURE_URL);

    // readyState >= 1 means the element parsed the container and knows the
    // duration -- proof it fetched and decoded, not merely that src was set.
    await expect
        .poll(() => player.evaluate((el: HTMLVideoElement) => el.readyState), { timeout: 10_000 })
        .toBeGreaterThanOrEqual(1);
    await expect
        .poll(() => player.evaluate((el: HTMLVideoElement) => el.duration))
        .toBeGreaterThan(0);

    await page.locator('#video-close').click();
    await expect(player).toBeHidden();

    // The session is the thing that leaks: a token left open holds a reader and
    // a connection pool for the life of the process.
    await expect.poll(async () => (await mock.calls('CloseMedia')).length).toBeGreaterThan(0);
});

for (const platform of ['android', 'ios'] as const) {
    test(`a touch drag scrubs the mobile timeline on ${platform}`, async ({ page, browserName }) => {
        await page.setViewportSize({ width: 390, height: 844 });
        await serveFixture(page);
        await bootTDrive(page, {
            GetFolderContents: resolves({ folders: [], files: [VIDEO_FILE] }),
            OpenMedia: resolves(opened()),
        }, { url: `/?mobile=${platform}` });
        await openTheVideo(page);

        const player = page.locator('#video-player');
        await expect.poll(() => player.evaluate((el: HTMLVideoElement) => el.duration)).toBeGreaterThan(0);
        await player.evaluate((el: HTMLVideoElement) => { el.pause(); el.currentTime = 0; });

        const scrubber = page.getByRole('slider', { name: 'Seek' });
        await expect(scrubber).toHaveCSS('touch-action', 'none');
        const bounds = await scrubber.boundingBox();
        if (!bounds) throw new Error('video scrubber is not visible');
        const y = bounds.y + bounds.height / 2;
        const start = bounds.x + bounds.width * 0.1;
        const end = bounds.x + bounds.width * 0.75;

        if (browserName === 'chromium') {
            const client = await page.context().newCDPSession(page);
            await client.send('Emulation.setTouchEmulationEnabled', { enabled: true });
            await client.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x: start, y, id: 1 }] });
            for (let step = 1; step <= 8; step += 1) {
                await client.send('Input.dispatchTouchEvent', {
                    type: 'touchMove',
                    touchPoints: [{ x: start + (end - start) * step / 8, y, id: 1 }],
                });
            }
            await expect(page.locator('#video-time')).toHaveText('0:01');
            await player.evaluate((el: HTMLVideoElement) => el.dispatchEvent(new Event('timeupdate')));
            await expect(page.locator('#video-time')).toHaveText('0:01');
            await expect(scrubber).toHaveAttribute('aria-valuetext', '0:01 of 0:02');
            await client.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });
            await client.detach();
        } else {
            await page.mouse.move(start, y);
            await page.mouse.down();
            await page.mouse.move(end, y, { steps: 8 });
            await expect(page.locator('#video-time')).toHaveText('0:01');
            await player.evaluate((el: HTMLVideoElement) => el.dispatchEvent(new Event('timeupdate')));
            await expect(page.locator('#video-time')).toHaveText('0:01');
            await expect(scrubber).toHaveAttribute('aria-valuetext', '0:01 of 0:02');
            await page.mouse.up();
        }

        await expect.poll(() => player.evaluate((el: HTMLVideoElement) => el.currentTime)).toBeGreaterThan(0.7);
    });
}

test('surfaces a failed open instead of leaving a dead player on screen', async ({ page }) => {
    await serveFixture(page);
    await bootTDrive(page, {
        GetFolderContents: resolves({ folders: [], files: [VIDEO_FILE] }),
        OpenMedia: rejects('Telegram is unavailable', 60),
    });

    await openTheVideo(page);

    // The reader gets normalized copy and a way out, not the transport's own
    // words -- what matters is that the failure lands on the error surface
    // rather than leaving an empty player behind.
    await expect(page.locator('#video-error')).toBeVisible();
    await expect(page.locator('#video-error-message')).not.toBeEmpty();
    await expect(page.locator('#video-error-retry')).toBeVisible();
    // The element stays mounted under the notice -- Retry reuses it -- but it
    // must not be holding a source from a session that never opened.
    await expect
        .poll(() => page.locator('#video-player').evaluate((el: HTMLVideoElement) => el.currentSrc))
        .toBe('');
});

test('keeps playing in the webview when the native player refuses to start', async ({ page }) => {
    await serveFixture(page);
    await bootTDrive(page, {
        GetFolderContents: resolves({ folders: [], files: [VIDEO_FILE] }),
        OpenMedia: resolves(opened()),
        // The promotion path: the backend opened the stream, then the
        // out-of-process player failed to attach. The reader should not care.
        OpenNativeMedia: rejects('mpv is not installed', 40),
    });

    await openTheVideo(page);

    const player = page.locator('#video-player');
    await expect(player).toBeVisible();
    await expect
        .poll(() => player.evaluate((el: HTMLVideoElement) => el.readyState), { timeout: 10_000 })
        .toBeGreaterThanOrEqual(1);
    await expect(page.locator('#video-error')).toBeHidden();
});

test('the speed control changes the element, not just the label', async ({ page }) => {
    await serveFixture(page);
    await bootTDrive(page, {
        GetFolderContents: resolves({ folders: [], files: [VIDEO_FILE] }),
        OpenMedia: resolves(opened()),
    });

    await openTheVideo(page);
    const player = page.locator('#video-player');
    await expect
        .poll(() => player.evaluate((el: HTMLVideoElement) => el.readyState), { timeout: 10_000 })
        .toBeGreaterThanOrEqual(1);

    const rate = () => player.evaluate((el: HTMLVideoElement) => el.playbackRate);
    const speedButton = page.locator('#video-speed-button');

    // The pill cycles rather than opening anything: one tap is the whole
    // interaction, and it has to reach the element, not just relabel itself.
    await speedButton.click();
    await expect.poll(rate).toBeGreaterThan(1);
    await expect(speedButton).toHaveText(new RegExp(`^${await rate()}x$`));

    // The exact rate lives in the settings panel, where the pill's cycle is
    // only a shortcut through the same state.
    await page.getByRole('button', { name: 'Playback settings' }).click();
    await page.locator('[data-settings-section="speed"]').click();
    const menu = page.locator('#video-speed-menu');
    await expect(menu).toBeVisible();
    await menu.getByRole('menuitemradio', { name: '1.5x' }).click();

    await expect.poll(rate).toBe(1.5);
    await expect(speedButton).toHaveText('1.5x');
});

test('on iOS the element is handed the HLS source, not the progressive one', async ({ page }) => {
    await serveFixture(page);

    // Record every source the element is given, rather than reading src after
    // the fact. The HLS URL resolves to nothing here, so the element's load
    // fails and the controller tears the session down -- correctly, but it
    // clears src on the way out, and whether the assertion lands before that
    // is a race. What is being tested is the choice, and the choice is made
    // the moment src is written.
    await page.addInitScript(() => {
        (window as unknown as { __videoSources: string[] }).__videoSources = [];
        new MutationObserver((records) => {
            for (const record of records) {
                const src = (record.target as HTMLVideoElement).getAttribute('src');
                if (src) (window as unknown as { __videoSources: string[] }).__videoSources.push(src);
            }
        }).observe(document, { subtree: true, attributes: true, attributeFilter: ['src'] });
    });

    // Both sources are offered, exactly as the backend offers them. The choice
    // is the frontend's, and getting it backwards on a real phone means a
    // player that loads its bytes and then stalls -- iOS Safari will not seek
    // a progressive stream the way the desktop webviews do.
    await bootTDrive(page, {
        GetFolderContents: resolves({ folders: [], files: [VIDEO_FILE] }),
        OpenMedia: resolves(opened({ hls_url: HLS_URL })),
    }, { url: '/?mobile=ios' });

    await openTheVideo(page);

    await expect
        .poll(() => page.evaluate(() => (window as unknown as { __videoSources: string[] }).__videoSources))
        .toContain(HLS_URL);
    // The progressive URL must never have been tried: reaching for it first
    // and falling back is the bug this guards.
    expect(await page.evaluate(() => (window as unknown as { __videoSources: string[] }).__videoSources))
        .not.toContain(FIXTURE_URL);
});

/**
 * A locked vault has to reach the reader as a prompt. The backend refuses an
 * encrypted open with its own "encryption password required", and the player
 * turns that into the unlock dialog rather than an error about a video that is
 * perfectly fine -- or, worse, a spinner that never resolves.
 */
test('an encrypted video with the vault locked asks for the password', async ({ page }) => {
    const encrypted = { ...VIDEO_FILE, name: 'holiday.mkv', msg_id: 611, encrypted: true, plaintext_size: VIDEO_FILE.size };
    const mock = await bootTDrive(page, {
        GetFolderContents: resolves({ folders: [], files: [encrypted] }),
        EncryptionStatus: resolves({ available: true, password_set: true, password_remembered: false, hint: '' }),
        // What the drive answers with while the key is not in memory.
        OpenMedia: rejects('media: encryption key unavailable: encryption password required'),
        OpenNativeMedia: rejects('media: encryption key unavailable: encryption password required'),
    });
    await page.getByRole('row', { name: 'File: holiday.mkv' }).getByRole('button', { name: 'Play video' }).click();

    await expect(page.getByRole('dialog', { name: 'Enter encryption password' })).toBeVisible();
    // Nothing was played, and dismissing the prompt is a choice rather than a
    // failure to report.
    await page.getByRole('dialog', { name: 'Enter encryption password' }).getByRole('button', { name: 'Cancel', exact: true }).click();
    expect(await mock.calls('AttachNativeMedia')).toHaveLength(0);
});
