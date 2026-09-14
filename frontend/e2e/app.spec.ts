import {
    bootTDrive,
    byFirstArg,
    expect,
    rejects,
    resolves,
    returnsSynchronously,
    test,
} from './wails-mock';

const RED_BASE64 = 'PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHdpZHRoPSIyIiBoZWlnaHQ9IjIiPjxyZWN0IHdpZHRoPSIyIiBoZWlnaHQ9IjIiIGZpbGw9IiNlMTFkNDgiLz48L3N2Zz4=';
const BLUE_BASE64 = 'PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHdpZHRoPSIyIiBoZWlnaHQ9IjIiPjxyZWN0IHdpZHRoPSIyIiBoZWlnaHQ9IjIiIGZpbGw9IiMyNTYzZWIiLz48L3N2Zz4=';
const GOLD_BASE64 = 'PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHdpZHRoPSIyIiBoZWlnaHQ9IjIiPjxyZWN0IHdpZHRoPSIyIiBoZWlnaHQ9IjIiIGZpbGw9IiNmNTllMGIiLz48L3N2Zz4=';
const RED_URL = `data:image/svg+xml;base64,${RED_BASE64}`;
const BLUE_URL = `data:image/svg+xml;base64,${BLUE_BASE64}`;
const GOLD_URL = `data:image/svg+xml;base64,${GOLD_BASE64}`;

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

test('moves row focus and reaches row actions with the keyboard', async ({ page }) => {
    await bootTDrive(page, {
        GetFolderContents: resolves({
            folders: [],
            files: [
                { name: 'older.txt', size: 1, msg_id: 1, parent_id: '', upload_time: 1, uploader_id: 7, encrypted: false, plaintext_size: 0 },
                { name: 'newer.txt', size: 2, msg_id: 2, parent_id: '', upload_time: 2, uploader_id: 7, encrypted: false, plaintext_size: 0 },
            ],
        }),
    });

    const newest = page.getByRole('row', { name: 'File: newer.txt' });
    const older = page.getByRole('row', { name: 'File: older.txt' });
    await expect(newest).toBeVisible();
    await newest.focus();
    await page.keyboard.press('ArrowDown');
    await expect(older).toBeFocused();
    await page.keyboard.press('Space');
    await expect(older).toHaveAttribute('aria-selected', 'true');
    await page.keyboard.press('ArrowRight');
    await expect(older.getByRole('button', { name: 'Open file' })).toBeFocused();
    await page.keyboard.press('ArrowLeft');
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



test('context menus and modals move focus, close on Escape, and restore the trigger', async ({ page }) => {
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
    });
    await expect(page.locator('#success-screen')).toBeVisible();

    const driveActions = page.getByRole('button', { name: 'Actions for Team' });
    await driveActions.click();
    const menu = page.getByRole('menu');
    await expect(menu).toBeVisible();
    await expect(menu.getByRole('menuitem').first()).toBeFocused();
    await page.keyboard.press('Escape');
    await expect(menu).toBeHidden();
    await expect(driveActions).toBeFocused();

    const newDrive = page.getByRole('button', { name: 'New shared drive' });
    await newDrive.click();
    const dialog = page.getByRole('dialog', { name: 'New shared drive' });
    await expect(dialog).toBeVisible();
    await expect(page.locator('#new-drive-name')).toBeFocused();
    await page.keyboard.press('Escape');
    await expect(dialog).toBeHidden();
    await expect(newDrive).toBeFocused();
});

test('gallery accepts a synchronous data-image thumbnail and previews the normalized payload', async ({ page }) => {
    const mock = await bootTDrive(page, {
        ListMedia: resolves([FIRST_PHOTO]),
        Thumbnail: returnsSynchronously({ data_base64: GOLD_BASE64, mime_type: 'image/svg+xml' }),
        PreviewFile: resolves({ result: { ok: true }, payload: { data_base64: RED_BASE64, mime_type: 'image/svg+xml' } }),
    });

    await expect(page.locator('#success-screen')).toBeVisible();
    await page.getByRole('button', { name: 'Photos' }).click();

    const photo = page.getByRole('button', { name: 'first.jpg' });
    await expect(photo).toBeVisible();
    await expect(photo.locator('img')).toHaveAttribute('src', GOLD_URL);
    expect(await mock.calls('Thumbnail')).toMatchObject([{ args: [101], state: 'returned' }]);

    await photo.click();
    await expect(page.getByRole('dialog', { name: 'first.jpg' })).toBeVisible();
    await expect(page.locator('#preview-image')).toHaveAttribute('src', RED_URL);
    await expect(page.locator('#preview-image')).toBeVisible();
    expect(await mock.calls('PreviewFile')).toMatchObject([{ args: [101], state: 'fulfilled' }]);
});

test('a late preview completion cannot overwrite rapid gallery navigation', async ({ page }) => {
    const mock = await bootTDrive(page, {
        ListMedia: resolves([FIRST_PHOTO, SECOND_PHOTO]),
        Thumbnail: byFirstArg({
            '101': returnsSynchronously({ data_base64: RED_BASE64, mime_type: 'image/svg+xml' }),
            '102': returnsSynchronously({ data_base64: BLUE_BASE64, mime_type: 'image/svg+xml' }),
        }),
        PreviewFile: byFirstArg({
            '101': resolves({ result: { ok: true }, payload: { data_base64: RED_BASE64, mime_type: 'image/svg+xml' } }, 1_000),
            '102': resolves({ result: { ok: true }, payload: { data_base64: BLUE_BASE64, mime_type: 'image/svg+xml' } }),
        }),
    });

    await expect(page.locator('#success-screen')).toBeVisible();
    await page.getByRole('button', { name: 'Photos' }).click();
    await expect(page.getByRole('button', { name: 'first.jpg' }).locator('img')).toHaveAttribute('src', RED_URL);
    await expect(page.getByRole('button', { name: 'second.jpg' }).locator('img')).toHaveAttribute('src', BLUE_URL);

    await page.getByRole('button', { name: 'first.jpg' }).click();
    await expect(page.getByRole('dialog', { name: 'first.jpg' })).toBeVisible();
    await page.getByRole('button', { name: 'Next image' }).click();
    await expect(page.getByRole('dialog', { name: 'second.jpg' })).toBeVisible();
    await expect(page.locator('#preview-image')).toHaveAttribute('src', BLUE_URL);

    await expect.poll(async () => {
        const firstCall = (await mock.calls('PreviewFile')).find((call) => call.args[0] === 101);
        return firstCall?.state;
    }).toBe('fulfilled');
    await expect(page.locator('#preview-image')).toHaveAttribute('src', BLUE_URL);
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
