import { bootTDrive, expect, resolves, test } from './wails-mock';

for (const platform of ['desktop', 'android', 'ios'] as const) {
    test(`photo backup settings and pause are available on ${platform}`, async ({ page }) => {
        if (platform !== 'desktop') {
            await page.setViewportSize({ width: 390, height: 844 });
            await page.addInitScript((mobile) => history.replaceState(null, '', `/?mobile=${mobile}`), platform);
        }
        const state = {
            platform,
            manual_paused: false,
            settings: { enabled: true, photos: true, videos: true, future_only: false, wifi_only: false, charging_only: false },
            sources: [{ id: 'camera', kind: 'library', root: 'Camera', name: 'Camera', enabled: true, added_at: 1 }],
            status: { phase: 'uploading', pending: 12, uploading: 1, complete: 24, failed: 0 },
            capabilities: {
                wifi_only: { supported: false, label: 'Unavailable on this device' },
                charging_only: { supported: false, label: 'Unavailable on this device' },
                access: { status: 'limited', detail: 'Only selected photos are accessible.' },
            },
        };
        const mock = await bootTDrive(page, {
            GetPhotoBackupState: resolves(state),
            PausePhotoBackup: resolves(null),
            ResumePhotoBackup: resolves(null),
        });
        if (platform === 'desktop') {
            await page.locator('#profile-trigger').click();
            await page.getByRole('menuitem', { name: 'Photo & video backup' }).click();
        } else {
            await page.getByRole('navigation', { name: 'Primary' }).getByRole('button', { name: 'Account', exact: true }).click();
        }
        const panel = page.getByRole('region', { name: 'Photo and video backup' });
        await expect(panel.getByRole('checkbox', { name: 'Photos', exact: true })).toBeChecked();
        await expect(panel.getByRole('checkbox', { name: 'Videos', exact: true })).toBeChecked();
        await expect(panel.getByRole('checkbox', { name: 'Wi-Fi only', exact: true })).toBeDisabled();
        await expect(panel).toContainText('Only selected photos are accessible');
        await expect(panel).toContainText('24 completed');
        await mock.setPlan('GetPhotoBackupState', resolves({
            ...state, manual_paused: true, status: { ...state.status, phase: 'paused', uploading: 0, pending: 13, message: 'Paused by you.' },
        }));
        await panel.getByRole('button', { name: 'Pause', exact: true }).click();
        await expect.poll(async () => (await mock.calls('PausePhotoBackup')).length).toBe(1);
        await expect(panel).toContainText('Paused by you.');
        await expect(panel.getByRole('button', { name: 'Resume', exact: true })).toBeVisible();
        await mock.emit('photo-backup:state');
        await expect(panel.getByRole('button', { name: 'Resume', exact: true })).toBeVisible();
        expect(await mock.calls('ResumePhotoBackup')).toHaveLength(0);
        await panel.getByRole('button', { name: 'Resume', exact: true }).click();
        await expect.poll(async () => (await mock.calls('ResumePhotoBackup')).length).toBe(1);
        await mock.setPlan('GetPhotoBackupState', resolves(state));
        await mock.emit('photo-backup:state');
        await expect(panel.getByRole('button', { name: 'Pause', exact: true })).toBeVisible();
        expect(await mock.calls('ClearGalleryCache')).toHaveLength(0);
    });
}
