import {
    bootTDrive,
    galleryPage,
    byFirstArg,
    expect,
    rejects,
    resolves,
    test,
} from './wails-mock';
import type { Page } from '@playwright/test';

const RED_BASE64 = 'PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHdpZHRoPSIyIiBoZWlnaHQ9IjIiPjxyZWN0IHdpZHRoPSIyIiBoZWlnaHQ9IjIiIGZpbGw9IiNlMTFkNDgiLz48L3N2Zz4=';
const BLUE_BASE64 = 'PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHdpZHRoPSIyIiBoZWlnaHQ9IjIiPjxyZWN0IHdpZHRoPSIyIiBoZWlnaHQ9IjIiIGZpbGw9IiMyNTYzZWIiLz48L3N2Zz4=';
const GOLD_BASE64 = 'PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHdpZHRoPSIyIiBoZWlnaHQ9IjIiPjxyZWN0IHdpZHRoPSIyIiBoZWlnaHQ9IjIiIGZpbGw9IiNmNTllMGIiLz48L3N2Zz4=';
const RED_PNG_BASE64 = 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGN4KOvxHwAFbwJG6bt5fAAAAABJRU5ErkJggg==';
const BLUE_PNG_BASE64 = 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGNQTX79HwAElwJzVt2vfAAAAABJRU5ErkJggg==';
const ROW_CAPABILITY = 'original-row';
const FIRST_CAPABILITY = 'original-101';
const SECOND_CAPABILITY = 'original-102';

const PERSONAL_CHANNEL = {
    id: 1,
    title: 'Personal',
    kind: 'personal',
    is_active: true,
    invite_link: '',
};

declare global {
    interface Window {
        __fileListLoadingStates?: string[];
        __fileListSnapshots?: string[];
    }
}

const FIRST_PHOTO = {
    name: 'first.jpg',
    size: 120,
    msg_id: 101,
    parent_id: '',
    upload_time: 1_735_689_600,
    uploader_id: 7,
    encrypted: false,
    plaintext_size: 0,
};

const SECOND_PHOTO = {
    name: 'second.jpg',
    size: 240,
    msg_id: 102,
    parent_id: '',
    upload_time: 1_732_924_800,
    uploader_id: 7,
    encrypted: false,
    plaintext_size: 0,
};

test('auth advances through the public login surface and handles runtime errors', async ({ page }) => {
    const mock = await bootTDrive(page, {
        CheckLoginStatus: resolves(false),
        LoginPhoneNumber: resolves(null),
    });

    const phone = page.getByRole('textbox', { name: 'Phone number' });
    await expect(page.getByRole('heading', { name: 'Sign in to Telegram' })).toBeVisible();
    await expect(phone).toBeFocused();

    await phone.fill('+1 555 0100');
    await page.getByRole('button', { name: 'Send code' }).click();
    await expect(page.getByRole('heading', { name: 'Verify your account' })).toBeVisible();
    await expect(page.getByText('+1 555 0100')).toBeVisible();
    expect(await mock.calls('LoginPhoneNumber')).toMatchObject([
        { args: ['+1 555 0100'], state: 'fulfilled' },
    ]);

    await mock.emit('login-code-invalid');
    await expect(page.locator('#code-error')).toContainText('That code was incorrect');
});

test('dashboard renders normalized first-party drive and file data', async ({ page }) => {
    const mock = await bootTDrive(page, {
        GetFolderContents: resolves({
            folders: [],
            files: [{
                name: 'contract.txt',
                size: 2048,
                msg_id: 41,
                parent_id: '',
                upload_time: 1_735_689_600,
                uploader_id: 7,
                encrypted: false,
                plaintext_size: 0,
            }],
        }),
    });

    await expect(page.locator('#success-screen')).toBeVisible();
    await expect(page.getByRole('button', { name: 'Personal', exact: true })).toHaveAttribute('aria-current', 'page');
    await expect(page.getByRole('row', { name: 'File: contract.txt' })).toBeVisible();
    await expect(page.locator('#storage-used')).toContainText('0 B / Unlimited');
    expect(await mock.calls('GetFolderContents')).toMatchObject([{ args: [''] }]);
});

test('publishes merged root sources once after delayed data resolves', async ({ page }) => {
    await page.addInitScript(() => {
        const snapshots: string[] = window.__fileListSnapshots = [];
        const attach = () => {
            const list = document.getElementById('file-list');
            if (!list || list.dataset.snapshotObserverAttached) return;
            list.dataset.snapshotObserverAttached = 'true';
            const capture = () => snapshots.push(Array.from(list.querySelectorAll<HTMLElement>('.drive-row'))
                .map((row) => row.dataset.name ?? '').join('|'));
            new MutationObserver(capture).observe(list, { childList: true, subtree: true });
        };
        const observeList = () => {
            new MutationObserver(attach).observe(document.documentElement, { childList: true, subtree: true });
            attach();
        };
        if (document.documentElement) observeList();
        else document.addEventListener('DOMContentLoaded', observeList, { once: true });
    });
    await bootTDrive(page, {
        GetFolderContents: resolves({
            folders: [],
            files: [{
                name: 'filesystem.txt', size: 1, msg_id: 41, parent_id: '', upload_time: 1_735_689_600,
                uploader_id: 7, encrypted: false, plaintext_size: 0,
            }],
        }, 120),
        GetFileList: resolves([{
            name: 'telegram.txt', size: 2, msg_id: 42, access_hash: 0, date: 1_735_689_601,
        }], 180),
    });

    await expect(page.getByRole('row', { name: 'File: filesystem.txt' })).toBeVisible();
    await expect(page.getByRole('row', { name: 'File: telegram.txt' })).toBeVisible();

    const snapshots = await page.evaluate(() => window.__fileListSnapshots ?? []);
    expect(snapshots.some((snapshot) => snapshot.includes('filesystem.txt') && !snapshot.includes('telegram.txt'))).toBe(false);
});

test('keeps foreground navigation failures visible', async ({ page }) => {
    await bootTDrive(page, {
        GetFolderContents: byFirstArg({
            '': resolves({ folders: [{ id: 'reports', name: 'Reports', parent_id: '' }], files: [] }),
            reports: rejects('Folder is unavailable', 120),
        }),
    });

    const reports = page.getByRole('row', { name: 'Folder: Reports' });
    await expect(reports).toBeVisible();
    await reports.dblclick();

    await expect(page.getByRole('alert')).toContainText('Folder is unavailable');
});

test('moves row focus and previews a selected image with Space', async ({ page }) => {
    await routeRenditions(page);
    await bootTDrive(page, {
        GetFolderContents: resolves({
            folders: [],
            files: [
                { name: 'older.jpg', size: 1, msg_id: 1, parent_id: '', upload_time: 1, uploader_id: 7, encrypted: false, plaintext_size: 0 },
                { name: 'newer.jpg', size: 2, msg_id: 2, parent_id: '', upload_time: 2, uploader_id: 7, encrypted: false, plaintext_size: 0 },
            ],
        }),
        OpenOriginalImage: resolves({ token: ROW_CAPABILITY, url: `data:image/png;base64,${RED_PNG_BASE64}`, thumbnail_url: '', hls_url: '', name: 'older.jpg', kind: 'image', mime_type: 'image/png', supports_range: true, info: { channel_id: 1, file_id: 1, revision: 1, name: 'older.jpg', stored_size: 70, plaintext_size: 70, encrypted: false, multipart: false } }),
    });

    const newest = page.getByRole('row', { name: 'File: newer.jpg' });
    const older = page.getByRole('row', { name: 'File: older.jpg' });
    await expect(newest).toBeVisible();
    await newest.focus();
    await page.keyboard.press('ArrowDown');
    await expect(older).toBeFocused();
    await page.keyboard.press('Space');
    await expect(older).toHaveAttribute('aria-selected', 'true');
    await page.keyboard.press('Space');
    await expect(page.getByRole('dialog', { name: 'older.jpg' })).toBeVisible();
    await expect(page.locator('#preview-image')).toHaveAttribute('src', `data:image/png;base64,${RED_PNG_BASE64}`);
    await page.keyboard.press('Escape');
    await expect(page.getByRole('dialog', { name: 'older.jpg' })).toBeHidden();
    await expect(older).toBeFocused();
});

test('windows large file lists while preserving endpoint keyboard focus', async ({ page }) => {
    await bootTDrive(page, {
        GetFolderContents: resolves({
            folders: [],
            files: Array.from({ length: 128 }, (_, index) => ({
                name: `entry-${String(index).padStart(3, '0')}.txt`,
                size: index + 1,
                msg_id: index + 1,
                parent_id: '',
                upload_time: index + 1,
                uploader_id: 7,
                encrypted: false,
                plaintext_size: 0,
            })),
        }),
    });

    const list = page.locator('#file-list');
    const newest = page.getByRole('row', { name: 'File: entry-127.txt' });
    const oldest = page.getByRole('row', { name: 'File: entry-000.txt' });
    await newest.focus();
    await page.keyboard.press('End');
    await expect(oldest).toBeFocused();
    expect(await list.locator('.drive-row').count()).toBeLessThan(64);
});

test('returning from Photos keeps the current file list while it refreshes', async ({ page }) => {
    const mock = await bootTDrive(page, {
        GetFolderContents: resolves({
            folders: [
                { id: 'alpha', name: 'Alpha', parent_id: '' },
                { id: 'zulu', name: 'Zulu', parent_id: '' },
            ],
            files: [{
                name: 'contract.txt',
                size: 2048,
                msg_id: 41,
                parent_id: '',
                upload_time: 1_735_689_600,
                uploader_id: 7,
                encrypted: false,
                plaintext_size: 0,
            }],
        }, 200),
        GetFolderStats: resolves([
            { id: 'alpha', bytes: 1024, latestUpload: 1_700_000_000 },
            { id: 'zulu', bytes: 2048, latestUpload: 1_735_689_600 },
        ], 400),
    });

    const file = page.getByRole('row', { name: 'File: contract.txt' });
    await expect(file).toBeVisible();
    const folders = page.locator('#file-list .folder-row');
    await expect(folders).toHaveCount(2);
    await expect(folders.nth(0)).toHaveAttribute('data-name', 'Zulu');
    await expect(folders.nth(0).locator('.folder-size')).toHaveText('2 KB');
    const initialFolderRequests = (await mock.calls('GetFolderContents')).length;
    await page.keyboard.press('Control+R');
    await expect.poll(async () => (await mock.calls('GetFolderContents'))[initialFolderRequests]?.state).toBe('pending');
    await page.getByRole('button', { name: 'Photos' }).click();
    await expect(page.locator('#gallery-view')).toBeVisible();
    const loadingStates: string[] = [];
    await page.locator('#file-list').evaluate((list) => {
        const states: string[] = window.__fileListLoadingStates = [];
        const snapshots: string[] = window.__fileListSnapshots = [];
        const record = () => {
            if (list.textContent?.includes('Loading files')) states.push(list.textContent);
            snapshots.push(Array.from(list.querySelectorAll<HTMLElement>('.drive-row'))
                .map((row) => row.dataset.name + ':' + Array.from(row.querySelectorAll('.row-meta')).map((meta) => meta.textContent?.trim()).join(':'))
                .join('|'));
        };
        new MutationObserver(record).observe(list, { childList: true, subtree: true, characterData: true });
    });

    await page.getByRole('button', { name: 'Personal', exact: true }).click();
    await page.waitForTimeout(800);
    loadingStates.push(...await page.evaluate(() => window.__fileListLoadingStates ?? []));
    const snapshots = await page.evaluate(() => window.__fileListSnapshots ?? []);

    expect(loadingStates).toEqual([]);
    expect(snapshots.some((snapshot) => snapshot.includes('Alpha:—:…') || snapshot.indexOf('Alpha:') < snapshot.indexOf('Zulu:'))).toBe(false);
    await expect(file).toBeVisible();
    await expect(folders.nth(0)).toHaveAttribute('data-name', 'Zulu');
    await expect(folders.nth(0).locator('.folder-size')).toHaveText('2 KB');
});

test('a Photos click wins over a pending drive switch', async ({ page }) => {
    await bootTDrive(page, {
        ListChannels: resolves([
            PERSONAL_CHANNEL,
            {
                id: 2,
                title: 'Team',
                kind: 'shared',
                is_active: false,
                invite_link: 'https://example.test/invite',
            },
        ]),
        SetActiveChannel: resolves(null, 400),
    });

    await expect(page.locator('#success-screen')).toBeVisible();
    await page.getByRole('button', { name: 'Team', exact: true }).click();
    await page.getByRole('button', { name: 'Photos' }).click();

    await expect(page.locator('#gallery-view')).toBeVisible();
    await expect(page.getByRole('button', { name: 'Photos' })).toHaveAttribute('aria-current', 'page');
    await page.waitForTimeout(500);
    await expect(page.locator('#gallery-view')).toBeVisible();
    await expect(page.getByRole('button', { name: 'Photos' })).toHaveAttribute('aria-current', 'page');
});



test('context menus retain vertical actions, render notifications, and restore focus', async ({ page }) => {
    await bootTDrive(page, {
        ListChannels: resolves([
            PERSONAL_CHANNEL,
            {
                id: 2,
                title: 'Team',
                kind: 'shared',
                is_active: false,
                invite_link: 'https://example.test/invite',
            },
        ]),
        GetInviteLink: rejects('Invite link unavailable'),
    });
    await expect(page.locator('#success-screen')).toBeVisible();

    const driveActions = page.getByRole('button', { name: 'Actions for Team' });
    await driveActions.click();
    const menu = page.getByRole('menu');
    await expect(menu).toBeVisible();
    const firstMenuItem = menu.getByRole('menuitem').first();
    const secondMenuItem = menu.getByRole('menuitem').nth(1);
    await expect(firstMenuItem).toBeFocused();
    const firstBox = await firstMenuItem.boundingBox();
    const secondBox = await secondMenuItem.boundingBox();
    expect(firstBox).not.toBeNull();
    expect(secondBox).not.toBeNull();
    expect(firstBox!.width).toBeGreaterThan(150);
    expect(secondBox!.y).toBeGreaterThan(firstBox!.y);
    await page.keyboard.press('Escape');
    await expect(menu).toBeHidden();
    await expect(driveActions).toBeFocused();

    await driveActions.click();
    await menu.getByRole('menuitem').first().click();
    const toastStack = page.locator('#toast-stack.toast-stack');
    await expect(toastStack).toHaveCSS('position', 'fixed');
    await expect(toastStack.getByRole('alert')).toContainText('Could not get invite link');

    const newDrive = page.getByRole('button', { name: 'New shared drive' });
    await newDrive.click();
    const dialog = page.getByRole('dialog', { name: 'New shared drive' });
    await expect(dialog).toBeVisible();
    await expect(page.locator('#new-drive-name')).toBeFocused();
    await page.keyboard.press('Escape');
    await expect(dialog).toBeHidden();
    await expect(newDrive).toBeFocused();
});

function galleryPlans(photos: typeof FIRST_PHOTO[], slowFirstOriginal = false) {
    const buckets: Array<{ key: string; start_index: number; count: number; upload_time: number }> = [];
    photos.forEach((photo, index) => {
        const key = new Date(photo.upload_time * 1000).toISOString().slice(0, 7);
        const previous = buckets[buckets.length - 1];
        if (previous?.key === key) previous.count += 1;
        else buckets.push({ key, start_index: index, count: 1, upload_time: photo.upload_time });
    });
    return {
        GetMediaTimeline: resolves({ channel_id: 1, generation: 'test', total_count: photos.length, page_size: 128, buckets, anchors: [{ start_index: 0, cursor: '0' }] }),
        ListMediaPage: resolves({ generation: 'test', start_index: 0, next_cursor: '', items: photos.map((photo) => ({ ...photo, revision: 1, content_msg_id: photo.msg_id, content_hash: '' })) }),
        OpenOriginalImage: byFirstArg({
            '101': resolves({ token: FIRST_CAPABILITY, url: `data:image/png;base64,${RED_PNG_BASE64}`, thumbnail_url: '', hls_url: '', name: 'first.jpg', kind: 'image', mime_type: 'image/png', supports_range: true, info: { channel_id: 1, file_id: 101, revision: 1, name: 'first.jpg', stored_size: 70, plaintext_size: 70, encrypted: false, multipart: false } }, slowFirstOriginal ? 1000 : 0),
            '102': resolves({ token: SECOND_CAPABILITY, url: `data:image/png;base64,${BLUE_PNG_BASE64}`, thumbnail_url: '', hls_url: '', name: 'second.jpg', kind: 'image', mime_type: 'image/png', supports_range: true, info: { channel_id: 1, file_id: 102, revision: 1, name: 'second.jpg', stored_size: 70, plaintext_size: 70, encrypted: false, multipart: false } }),
        }),
    };
}

async function routeRenditions(page: Page) {
    const requested: string[] = [];
    await page.route('**/mock-renditions/**', async (route) => {
        const url = new URL(route.request().url());
        requested.push(url.pathname);
        const color = url.pathname.includes('/102/') ? BLUE_BASE64 : GOLD_BASE64;
        await route.fulfill({ body: Buffer.from(color, 'base64'), contentType: 'image/svg+xml', headers: { 'X-Rendition-Width': '2', 'X-Rendition-Height': '2' } }).catch(() => {});
    });
    return requested;
}

test('gallery loads binary thumbnails and one explicitly opened original stream', async ({ page }) => {
    const requested = await routeRenditions(page);
    const mock = await bootTDrive(page, galleryPlans([FIRST_PHOTO]));
    await page.getByRole('button', { name: 'Photos' }).click();
    const photo = page.getByRole('button', { name: 'first.jpg' });
    await expect(photo).toBeVisible();
    await expect(photo.locator('img')).toHaveAttribute('src', /^blob:/);
    expect(await mock.calls('Thumbnail')).toEqual([]);
    expect(await mock.calls('ListMedia')).toEqual([]);
    expect(requested).toContain('/mock-renditions/101/thumbnail');
    await photo.click();
    await expect(page.getByRole('dialog', { name: 'first.jpg' })).toBeVisible();
    await expect(page.locator('#preview-image')).toHaveAttribute('src', `data:image/png;base64,${RED_PNG_BASE64}`);
    expect(await mock.calls('PreviewFile')).toEqual([]);
    expect(await mock.calls('OpenOriginalImage')).toMatchObject([{ args: [101, 1], state: 'fulfilled' }]);
    expect(requested.some((path) => path.endsWith('/preview'))).toBe(false);
});

test('mixed gallery loads video thumbnails before opening the streaming player', async ({ page }) => {
    const requested = await routeRenditions(page);
    const clip = { ...SECOND_PHOTO, name: 'holiday.mp4', size: 80_000_000 };
    const mock = await bootTDrive(page, {
        ...galleryPlans([FIRST_PHOTO, clip]),
        OpenMedia: rejects('Test stream unavailable'),
    });
    await page.getByRole('button', { name: 'Photos', exact: true }).click();
    const video = page.getByRole('button', { name: 'Video: holiday.mp4', exact: true });
    await expect(video).toBeVisible();
    await expect(video.locator('img')).toHaveAttribute('src', /^blob:/);
    expect(requested).toContain('/mock-renditions/102/thumbnail');
    expect(await mock.calls('OpenMedia')).toHaveLength(0);
    expect(await mock.calls('OpenOriginalImage')).toHaveLength(0);
    await video.click();
    await expect(page.locator('#video-modal')).toBeVisible();
    await expect.poll(async () => (await mock.calls('OpenMedia')).length).toBe(1);
    expect(await mock.calls('OpenOriginalImage')).toHaveLength(0);
    expect(requested.every((path) => path.endsWith('/thumbnail'))).toBe(true);
});

test('a late original completion cannot overwrite rapid gallery navigation', async ({ page }) => {
    await routeRenditions(page);
    await bootTDrive(page, galleryPlans([FIRST_PHOTO, SECOND_PHOTO], true));
    await page.getByRole('button', { name: 'Photos' }).click();
    await expect(page.getByRole('button', { name: 'first.jpg' }).locator('img')).toHaveAttribute('src', /^blob:/);
    await page.getByRole('button', { name: 'first.jpg' }).click();
    await expect(page.getByRole('dialog', { name: 'first.jpg' })).toBeVisible();
    await page.getByRole('button', { name: 'Next image' }).click();
    await expect(page.getByRole('dialog', { name: 'second.jpg' })).toBeVisible();
    await expect(page.locator('#preview-image')).toHaveAttribute('src', `data:image/png;base64,${BLUE_PNG_BASE64}`);
    await page.waitForTimeout(1100);
    await expect(page.locator('#preview-image')).toHaveAttribute('src', `data:image/png;base64,${BLUE_PNG_BASE64}`);
    await expect(page.locator('#preview-filename')).toHaveText('second.jpg');
});

test('startup failures show the fatal recovery screen', async ({ page }) => {
    await bootTDrive(page, {
        CheckSystemStatus: rejects('mock startup failure'),
    });

    const recovery = page.getByRole('alert').filter({ hasText: 'TDrive could not start' });
    await expect(recovery).toBeVisible();
    await expect(recovery).toContainText("TDrive could not finish starting. Reload the app and try again.");

    await expect(recovery.getByRole('button', { name: 'Reload TDrive' })).toBeVisible();
});

test('prefers-reduced-motion disables entrance motion in Chromium', async ({ page }) => {
    await page.emulateMedia({ reducedMotion: 'reduce' });
    await bootTDrive(page, { CheckLoginStatus: resolves(false) });

    const motion = await page.locator('.auth-box').evaluate((element) => {
        const style = getComputedStyle(element);
        return {
            matches: matchMedia('(prefers-reduced-motion: reduce)').matches,
            animationName: style.animationName,
            transitionDuration: style.transitionDuration,
            scrollBehavior: getComputedStyle(document.documentElement).scrollBehavior,
        };
    });

    expect(motion.matches).toBe(true);
    expect(motion.animationName).toBe('none');
    expect(motion.scrollBehavior).toBe('auto');
    expect(Number.parseFloat(motion.transitionDuration) * 1_000).toBeLessThanOrEqual(0.01);
});

for (const platform of ['desktop', 'android', 'ios'] as const) {
    test(`photo cache is managed automatically on ${platform}`, async ({ page }, testInfo) => {
        if (platform !== 'desktop') {
            await page.setViewportSize({ width: 390, height: 844 });
            await page.addInitScript((mobile) => history.replaceState(null, '', `/?mobile=${mobile}`), platform);
        }
        const mock = await bootTDrive(page);
        if (platform === 'desktop') {
            await page.locator('#profile-trigger').click();
            await page.getByRole('menuitem', { name: 'Local storage' }).click();
        } else {
            await page.getByRole('navigation', { name: 'Primary' }).getByRole('button', { name: 'Account', exact: true }).click();
        }
        const panel = page.getByRole('region', { name: 'Local photo storage' });
        await expect(panel).toContainText('2 KB');
        await expect(panel).toContainText('removed automatically');
        await expect(page.getByRole('button', { name: /clear.*cache/i })).toHaveCount(0);
        expect(await mock.calls('ClearGalleryCache')).toHaveLength(0);
        const shot = testInfo.outputPath(`storage-${platform}.png`);
        await page.screenshot({ path: shot });
        await testInfo.attach(`Storage ${platform}`, { path: shot, contentType: 'image/png' });
    });

    test(`1M photo gallery stays bounded while scrolling, reversing and keyboard jumping on ${platform}`, async ({ page }, testInfo) => {
        if (platform !== 'desktop') {
            await page.setViewportSize({ width: 390, height: 844 });
            await page.addInitScript((mobile) => history.replaceState(null, '', `/?mobile=${mobile}`), platform);
        }
        await routeRenditions(page);
        const count = 1_000_000;
        const mock = await bootTDrive(page, {
            GetMediaTimelineSummary: resolves({ channel_id: 1, generation: 'test', total_count: count, page_size: 128,
                buckets: [{ key: '2025-01', start_index: 0, count, upload_time: FIRST_PHOTO.upload_time }], anchors: [],
            }),
            GetMediaTimeline: resolves({ channel_id: 1, generation: 'test', total_count: count, page_size: 128,
                buckets: [{ key: '2025-01', start_index: 0, count, upload_time: FIRST_PHOTO.upload_time }],
                anchors: Array.from({ length: Math.ceil(count / 128) }, (_, index) => ({ start_index: index * 128, cursor: String(index * 128) })),
            }),
            ListMediaPage: galleryPage(count, FIRST_PHOTO),
        });
        const photos = platform === 'desktop' ? page.getByRole('button', { name: 'Photos', exact: true })
            : page.getByRole('navigation', { name: 'Primary' }).getByRole('button', { name: 'Photos', exact: true });
        await photos.click();
        const gallery = page.locator('#gallery-view');
        await expect(gallery.locator('[data-id="1000"]')).toBeVisible();
        await expect(gallery.locator('[data-id="1000"] img')).toHaveAttribute('src', /^blob:/);
        const bounds = async () => {
            expect(await gallery.locator('.gallery-cell').count()).toBeLessThan(120);
            expect(await gallery.locator('*').count()).toBeLessThan(450);
        };
        await bounds();
        await gallery.evaluate((element) => { element.scrollTop = element.scrollHeight / 2; });
        await expect(gallery.locator('[data-id="1000"]')).toHaveCount(0);
        await expect.poll(async () => (await mock.calls('ListMediaPage')).length).toBeGreaterThan(1);
        await bounds();
        await gallery.evaluate((element) => { element.scrollTop = element.scrollHeight; });
        await expect(gallery.locator(`[data-id="${count + 999}"]`)).toBeVisible();
        await bounds();
        await gallery.evaluate((element) => { element.scrollTop = 0; });
        await expect(gallery.locator('[data-id="1000"]')).toBeVisible();
        const first = gallery.locator('[data-id="1000"]');
        await first.focus();
        await page.keyboard.press('End');
        await expect(gallery.locator(`[data-id="${count + 999}"]`)).toBeFocused();
        await page.keyboard.press('Home');
        await expect(gallery.locator('[data-id="1000"]')).toBeFocused();
        await bounds();
        expect(await mock.calls('ListMedia')).toEqual([]);
        expect(await mock.calls('Thumbnail')).toEqual([]);
        expect(await mock.calls('PreviewFile')).toEqual([]);
        expect((await mock.calls('ListMediaPage')).every((call) => call.args[1] === 128)).toBe(true);
        const shot = testInfo.outputPath(`gallery-${platform}.png`);
        await page.screenshot({ path: shot });
        await testInfo.attach(`Gallery ${platform}`, { path: shot, contentType: 'image/png' });
    });
}
