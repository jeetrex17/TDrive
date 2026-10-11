import { bootTDrive, deferred, expect, resolves, test } from './wails-mock';
import type { Page } from '@playwright/test';
import { FIRST_PHOTO, SECOND_PHOTO, galleryPlans, previewImageContents, routeRenditions } from './gallery-fixtures';

const locked = { available: true, password_set: true, password_remembered: false, hint: 'My hint' };
const biometric = { available: true, enrolled: false, kind: 'touch_id', reason: '' };
const backup = {
    platform: 'darwin', encryption_required: true,
    settings: { enabled: true, photos: true, videos: true, encrypt: true },
    sources: [{ id: 'pictures', name: 'Pictures', enabled: true }],
    status: { phase: 'paused', pending: 2, message: 'Unlock encryption to back up your photos and videos.' },
};

test('the gallery lightbox offers explicit Touch ID with password fallback and reuses the unlocked session', async ({ page }) => {
    await routeRenditions(page);
    const first = { ...FIRST_PHOTO, encrypted: true };
    const second = { ...SECOND_PHOTO, encrypted: true };
    const mock = await bootTDrive(page, {
        ...galleryPlans([first, second]), EncryptionStatus: resolves(locked),
        BiometricStatus: resolves({ ...biometric, enrolled: true }), UseBiometricUnlock: resolves({ ok: true }),
    });
    await page.getByRole('button', { name: 'Photos', exact: true }).click();
    await page.getByRole('button', { name: first.name, exact: true }).click();
    const photo = page.getByRole('dialog', { name: first.name });
    const card = photo.getByRole('group', { name: 'Locked photo' });
    await expect(card.getByRole('button', { name: 'Use Touch ID', exact: true })).toBeVisible();
    await expect(card.getByLabel('Encryption password', { exact: true })).toBeVisible();
    expect(await mock.calls('UseBiometricUnlock')).toHaveLength(0);
    expect(await mock.calls('OpenOriginalImage')).toHaveLength(0);
    await page.screenshot({ path: '/tmp/tdrive-biometric-gallery-desktop.png' });
    await mock.setPlan('EncryptionStatus', resolves({ ...locked, password_remembered: true }));
    await card.getByRole('button', { name: 'Use Touch ID', exact: true }).click();
    await expect.poll(() => previewImageContents(page)).toContain('#e11d48');
    expect(await mock.calls('UseBiometricUnlock')).toHaveLength(1);
    await page.keyboard.press('ArrowRight');
    await expect(page.locator('#preview-filename')).toHaveText(second.name);
    await expect.poll(() => previewImageContents(page)).toContain('#2563eb');
    expect(await mock.calls('UseBiometricUnlock')).toHaveLength(1);
    await expect(page.locator('#preview-locked')).toBeHidden();
});

for (const platform of ['desktop', 'android'] as const) {
    test(`the gallery lightbox keeps password fallback after failed biometric authentication on ${platform}`, async ({ page }) => {
        if (platform === 'android') await page.setViewportSize({ width: 390, height: 844 });
        await routeRenditions(page);
        const photo = { ...FIRST_PHOTO, encrypted: true };
        const mock = await bootTDrive(page, {
            ...galleryPlans([photo]), EncryptionStatus: resolves(locked),
            BiometricStatus: resolves({ ...biometric, kind: platform === 'android' ? 'biometric' : 'touch_id', enrolled: true }),
            UseBiometricUnlock: resolves({ ok: false, error: { code: 'biometric_failed', message: 'Private native diagnostic' } }),
            UseEncryptionPassword: resolves({ ok: true }),
        }, { url: platform === 'android' ? '/?mobile=android' : '/' });
        await page.getByRole('button', { name: 'Photos', exact: true }).click();
        await page.getByRole('button', { name: photo.name, exact: true }).click();
        const card = page.getByRole('group', { name: 'Locked photo' });
        const native = card.getByRole('button', { name: platform === 'android' ? 'Use biometrics' : 'Use Touch ID', exact: true });
        await native.click();
        await expect(card.getByRole('alert')).toHaveText('Biometric authentication failed. Try again or use your encryption password.');
        await expect(card).not.toContainText('Private native diagnostic');
        await expect(card.getByLabel('Encryption password', { exact: true })).toBeEnabled();
        expect(await mock.calls('OpenOriginalImage')).toHaveLength(0);
        expect(await mock.calls('UseBiometricUnlock')).toHaveLength(1);
        const bounds = (await card.locator('.preview-unlock').boundingBox())!;
        expect(bounds.x).toBeGreaterThanOrEqual(0);
        expect(bounds.x + bounds.width).toBeLessThanOrEqual(platform === 'android' ? 390 : 1280);
        expect((await native.boundingBox())!.height).toBeGreaterThanOrEqual(44);
        await page.screenshot({ path: `/tmp/tdrive-biometric-gallery-${platform}.png` });
        await card.getByLabel('Encryption password', { exact: true }).fill('fallback-password');
        await mock.setPlan('EncryptionStatus', resolves({ ...locked, password_remembered: true }));
        await card.getByLabel('Encryption password', { exact: true }).press('Enter');
        await expect.poll(() => previewImageContents(page)).toContain('#e11d48');
        expect((await mock.calls('UseEncryptionPassword'))[0].args).toEqual(['fallback-password']);
        expect(await mock.calls('UseBiometricUnlock')).toHaveLength(1);
        await expect(page.locator('#preview-locked-input')).toHaveValue('');
    });
}

for (const transition of ['close', 'navigation', 'scope reset'] as const) {
    test(`the gallery lightbox cancels biometric work on ${transition} and ignores its late success`, async ({ page }) => {
        await routeRenditions(page);
        const first = { ...FIRST_PHOTO, encrypted: true };
        const mock = await bootTDrive(page, {
            ...galleryPlans([first, SECOND_PHOTO]), EncryptionStatus: resolves(locked),
            BiometricStatus: resolves({ ...biometric, enrolled: true }),
            UseBiometricUnlock: deferred('old-gallery-auth', { ok: true }), CancelBiometricUnlock: resolves({ ok: true }),
        });
        await page.getByRole('button', { name: 'Photos', exact: true }).click();
        await page.getByRole('button', { name: first.name, exact: true }).click();
        await page.getByRole('group', { name: 'Locked photo' }).getByRole('button', { name: 'Use Touch ID', exact: true }).click();
        await expect.poll(async () => (await mock.calls('UseBiometricUnlock')).length).toBe(1);
        if (transition === 'close') await page.getByRole('button', { name: 'Close preview', exact: true }).click();
        else if (transition === 'navigation') {
            await page.getByRole('button', { name: 'Next image', exact: true }).click();
            await expect(page.locator('#preview-filename')).toHaveText(SECOND_PHOTO.name);
            await expect.poll(() => previewImageContents(page)).toContain('#2563eb');
        } else await mock.emit('encrypted_media_sessions_closed');
        await expect.poll(async () => (await mock.calls('CancelBiometricUnlock')).length).toBe(1);
        await mock.release('old-gallery-auth');
        await expect.poll(async () => (await mock.calls('UseBiometricUnlock'))[0].state).toBe('fulfilled');
        expect(await mock.calls('OpenOriginalImage')).toHaveLength(transition === 'navigation' ? 1 : 0);
        if (transition === 'navigation') await expect(page.locator('#preview-filename')).toHaveText(SECOND_PHOTO.name);
        else await expect(page.locator('#preview-modal')).toBeHidden();
    });
}

test('the gallery lightbox discards late biometric capability from the previous photo', async ({ page }) => {
    await routeRenditions(page);
    const first = { ...FIRST_PHOTO, encrypted: true };
    const second = { ...SECOND_PHOTO, encrypted: true };
    const mock = await bootTDrive(page, {
        ...galleryPlans([first, second]), EncryptionStatus: resolves(locked),
        BiometricStatus: deferred('old-gallery-capability', { ...biometric, enrolled: true }),
    });
    await page.getByRole('button', { name: 'Photos', exact: true }).click();
    await page.getByRole('button', { name: first.name, exact: true }).click();
    await expect.poll(async () => (await mock.calls('BiometricStatus')).length).toBe(1);
    await mock.setPlan('BiometricStatus', resolves({ ...biometric, available: false, enrolled: false, reason: 'unsupported' }));
    await page.getByRole('button', { name: 'Next image', exact: true }).click();
    await expect(page.locator('#preview-filename')).toHaveText(second.name);
    await expect.poll(async () => (await mock.calls('BiometricStatus')).length).toBe(2);
    await mock.release('old-gallery-capability');
    await expect.poll(async () => (await mock.calls('BiometricStatus'))[0].state).toBe('fulfilled');
    await expect(page.getByRole('group', { name: 'Locked photo' }).getByRole('button', { name: 'Use Touch ID', exact: true })).toHaveCount(0);
    await expect(page.getByRole('group', { name: 'Locked photo' }).getByLabel('Encryption password', { exact: true })).toBeEnabled();
    expect(await mock.calls('UseBiometricUnlock')).toHaveLength(0);
});

async function openBackup(page: Page) {
    await page.locator('#profile-trigger').click();
    await page.getByRole('menuitem', { name: 'Photo & video backup' }).click();
    return page.getByRole('region', { name: 'Photo and video backup' });
}

test('device enrollment is explicit and Touch ID unlock is reused for the current session', async ({ page }, testInfo) => {
    const mock = await bootTDrive(page, {
        EncryptionStatus: resolves(locked), BiometricStatus: resolves(biometric),
        EnableBiometricUnlock: resolves({ ok: true }), UseBiometricUnlock: resolves({ ok: true }),
        GetPhotoBackupState: resolves(backup), RunPhotoBackup: resolves({ ok: true }),
    });
    await page.locator('#profile-trigger').click();
    await page.locator('#profile-menu-encryption-settings').click();
    const settings = page.getByRole('dialog', { name: 'Encryption settings' });
    await expect(settings).toBeVisible();
    await settings.getByRole('button', { name: 'Device unlock', exact: true }).click();
    await expect(settings.getByRole('button', { name: 'Enable Touch ID', exact: true })).toBeVisible();
    expect(await mock.calls('EnableBiometricUnlock')).toHaveLength(0);

    await settings.getByRole('button', { name: 'Enable Touch ID', exact: true }).click();
    await expect(settings.getByRole('alert')).toContainText('Enter your encryption password');
    expect(await mock.calls('EnableBiometricUnlock')).toHaveLength(0);
    await settings.locator('#encryption-biometric-password').fill('fresh-device-password');
    await mock.setPlan('BiometricStatus', resolves({ ...biometric, enrolled: true }));
    await settings.getByRole('button', { name: 'Enable Touch ID', exact: true }).click();
    await expect(settings.getByRole('button', { name: 'Disable Touch ID', exact: true })).toBeVisible();
    expect((await mock.calls('EnableBiometricUnlock'))[0].args).toEqual(['fresh-device-password']);
    await settings.getByRole('button', { name: 'Cancel', exact: true }).click();

    const panel = await openBackup(page);
    await panel.getByRole('button', { name: 'Unlock and back up', exact: true }).click();
    const prompt = page.getByRole('dialog', { name: 'Unlock encrypted files' });
    await expect(prompt.getByRole('button', { name: 'Use Touch ID', exact: true })).toBeVisible();
    await expect(prompt.getByLabel('Encryption password', { exact: true })).toBeVisible();
    await page.screenshot({ path: testInfo.outputPath('touch-id-unlock.png') });
    await mock.setPlan('EncryptionStatus', resolves({ ...locked, password_remembered: true }));
    await prompt.getByRole('button', { name: 'Use Touch ID', exact: true }).click();
    await expect(prompt).toBeHidden();
    await expect.poll(async () => (await mock.calls('RunPhotoBackup')).length).toBe(1);
    expect(await mock.calls('UseBiometricUnlock')).toHaveLength(1);

    await openBackup(page);
    await panel.getByRole('button', { name: 'Unlock and back up', exact: true }).click();
    await expect.poll(async () => (await mock.calls('RunPhotoBackup')).length).toBe(2);
    await expect(prompt).toBeHidden();
    expect(await mock.calls('UseBiometricUnlock')).toHaveLength(1);
});

test('canceling native authentication keeps encrypted work stopped and discards a late success', async ({ page }) => {
    const mock = await bootTDrive(page, {
        EncryptionStatus: resolves(locked), BiometricStatus: resolves({ ...biometric, enrolled: true }),
        UseBiometricUnlock: deferred('old-native-auth', { ok: true }), CancelBiometricUnlock: resolves({ ok: true }),
        GetPhotoBackupState: resolves(backup),
    });
    const panel = await openBackup(page);
    const prompt = page.getByRole('dialog', { name: 'Unlock encrypted files' });
    await panel.getByRole('button', { name: 'Unlock and back up', exact: true }).click();
    await prompt.getByRole('button', { name: 'Use Touch ID', exact: true }).click();
    await expect(prompt.getByRole('button', { name: 'Cancel', exact: true })).toBeEnabled();
    await prompt.getByRole('button', { name: 'Cancel', exact: true }).click();
    await expect(prompt).toBeHidden();
    await expect.poll(async () => (await mock.calls('CancelBiometricUnlock')).length).toBe(1);
    await openBackup(page);
    await panel.getByRole('button', { name: 'Unlock and back up', exact: true }).click();
    await expect(prompt).toBeVisible();
    await mock.release('old-native-auth');
    await expect.poll(async () => (await mock.calls('UseBiometricUnlock'))[0].state).toBe('fulfilled');
    await expect(prompt).toBeVisible();
    expect(await mock.calls('RunPhotoBackup')).toHaveLength(0);
    await prompt.getByRole('button', { name: 'Cancel', exact: true }).click();
});

test('canceled enrollment reports remaining device access when native cleanup fails', async ({ page }) => {
    const mock = await bootTDrive(page, {
        EncryptionStatus: resolves(locked), BiometricStatus: resolves(biometric),
        EnableBiometricUnlock: deferred('cleanup-failure', { ok: false, error: { code: 'biometric_cleanup_required', message: 'Private native details' } }),
        CancelBiometricUnlock: resolves({ ok: true }),
    });
    await page.locator('#profile-trigger').click();
    await page.locator('#profile-menu-encryption-settings').click();
    const settings = page.getByRole('dialog', { name: 'Encryption settings' });
    await settings.getByRole('button', { name: 'Device unlock', exact: true }).click();
    await settings.locator('#encryption-biometric-password').fill('device-password');
    await settings.getByRole('button', { name: 'Enable Touch ID', exact: true }).click();
    await expect.poll(async () => (await mock.calls('EnableBiometricUnlock')).length).toBe(1);
    await settings.getByRole('button', { name: 'Cancel', exact: true }).click();
    await expect(settings).toBeHidden();
    await mock.release('cleanup-failure');
    await expect(page.getByText('Biometric enrollment could not be removed. Open encryption settings and disable biometric unlock before trying again.', { exact: true })).toBeVisible();
    await expect(page.getByText('Private native details', { exact: true })).toHaveCount(0);
    await expect(settings).toBeHidden();
});

for (const platform of ['desktop', 'android'] as const) {
    test(`device enrollment stays accessible and can be canceled on ${platform}`, async ({ page }) => {
        await page.setViewportSize(platform === 'android' ? { width: 390, height: 844 } : { width: 1024, height: 768 });
        const mock = await bootTDrive(page, {
            EncryptionStatus: resolves(locked), BiometricStatus: resolves({ ...biometric, kind: platform === 'android' ? 'biometric' : 'touch_id' }),
            EnableBiometricUnlock: deferred('device-enrollment', { ok: true }), CancelBiometricUnlock: resolves({ ok: true }),
        }, { url: platform === 'android' ? '/?mobile=android' : '/' });
        if (platform === 'android') {
            await page.getByRole('navigation', { name: 'Primary' }).getByRole('button', { name: 'Account', exact: true }).click();
            await page.getByRole('button', { name: 'Encryption settings', exact: true }).click();
        } else {
            await page.locator('#profile-trigger').click();
            await page.locator('#profile-menu-encryption-settings').click();
        }
        const settings = page.getByRole('dialog', { name: 'Encryption settings' });
        await expect(settings).toBeVisible();
        await expect(settings.locator('#encryption-current-password')).toBeFocused();
        await settings.getByRole('button', { name: 'Device unlock', exact: true }).click();
        await expect(settings.getByRole('button', { name: /^Enable / })).toBeVisible();
        await settings.locator('#encryption-biometric-password').fill('device-password');
        await expect.poll(async () => {
            const box = (await settings.boundingBox())!;
            return box.y + box.height;
        }).toBeLessThanOrEqual(platform === 'android' ? 844 : 768);
        const bounds = (await settings.boundingBox())!;
        expect(bounds.x).toBeGreaterThanOrEqual(0);
        expect(bounds.y).toBeGreaterThanOrEqual(0);
        expect(bounds.y + bounds.height).toBeLessThanOrEqual(platform === 'android' ? 844 : 768);
        expect(await settings.evaluate((node) => node.scrollWidth <= node.clientWidth + 1)).toBe(true);
        await page.screenshot({ path: `/tmp/tdrive-biometric-settings-${platform}.png` });
        await settings.getByRole('button', { name: /^Enable / }).click();
        await expect.poll(async () => (await mock.calls('EnableBiometricUnlock')).length).toBe(1);
        await expect(settings.locator('#encryption-biometric-password')).toHaveValue('');
        await expect(settings.getByRole('button', { name: 'Cancel', exact: true })).toBeEnabled();
        await settings.getByRole('button', { name: 'Cancel', exact: true }).click();
        await expect(settings).toBeHidden();
        await expect.poll(async () => (await mock.calls('CancelBiometricUnlock')).length).toBe(1);
        await mock.release('device-enrollment');
        await expect(settings).toBeHidden();
    });

    test(`password fallback works after a canceled biometric prompt on ${platform}`, async ({ page }, testInfo) => {
        if (platform === 'android') await page.setViewportSize({ width: 390, height: 844 });
        const mock = await bootTDrive(page, {
            EncryptionStatus: resolves(locked),
            BiometricStatus: resolves({ ...biometric, kind: platform === 'android' ? 'biometric' : 'windows_hello', enrolled: true }),
            UseBiometricUnlock: resolves({ ok: false, error: { code: 'biometric_canceled', message: 'Native details must stay private' } }),
            UseEncryptionPassword: resolves({ ok: true }), GetPhotoBackupState: resolves({ ...backup, platform }),
            RunPhotoBackup: resolves({ ok: true }),
        }, { url: platform === 'android' ? '/?mobile=android' : '/' });
        let panel;
        if (platform === 'android') {
            await page.getByRole('navigation', { name: 'Primary' }).getByRole('button', { name: 'Account', exact: true }).click();
            await page.getByRole('button', { name: /Photo & video backup/ }).click();
            panel = page.getByRole('region', { name: 'Photo and video backup' });
        } else panel = await openBackup(page);
        await panel.getByRole('button', { name: 'Unlock and back up', exact: true }).click();
        const prompt = page.getByRole('dialog', { name: 'Unlock encrypted files' });
        await prompt.getByRole('button', { name: platform === 'android' ? 'Use biometrics' : 'Use Windows Hello', exact: true }).click();
        await expect(prompt.getByRole('alert')).toHaveText('Biometric unlock was canceled. Enter your password or try again.');
        await expect(prompt).not.toContainText('Native details');
        expect(await mock.calls('RunPhotoBackup')).toHaveLength(0);
        await page.screenshot({ path: testInfo.outputPath(`biometric-fallback-${platform}.png`) });
        await page.screenshot({ path: `/tmp/tdrive-biometric-unlock-${platform}.png` });
        await prompt.getByLabel('Encryption password', { exact: true }).fill('fallback-password');
        await mock.setPlan('EncryptionStatus', resolves({ ...locked, password_remembered: true }));
        await prompt.getByRole('button', { name: 'Unlock', exact: true }).click();
        await expect(prompt).toBeHidden();
        await expect.poll(async () => (await mock.calls('RunPhotoBackup')).length).toBe(1);
        expect((await mock.calls('UseEncryptionPassword'))[0].args).toEqual(['fallback-password']);
        expect(await mock.calls('UseBiometricUnlock')).toHaveLength(1);
    });
}
