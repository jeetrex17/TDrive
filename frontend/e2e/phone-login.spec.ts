import { bootTDrive, expect, resolves, test } from './wails-mock';

for (const platform of ['desktop', 'android', 'ios']) {
    test(`country picker submits an international number on ${platform}`, async ({ page }) => {
        if (platform !== 'desktop') {
            await page.setViewportSize({ width: 390, height: 844 });
            await page.addInitScript((mode) => history.replaceState(null, '', `/?mobile=${mode}`), platform);
        }
        const mock = await bootTDrive(page, { CheckLoginStatus: resolves(false), LoginPhoneNumber: resolves(null) });
        await expect(page.locator('#success-screen')).toBeHidden();
        await page.getByRole('button', { name: 'Choose country code' }).click();
        const search = page.getByRole('searchbox', { name: 'Search country or calling code' });
        await expect(search).toBeFocused();
        await search.fill('india');
        await page.getByRole('button', { name: 'India +91', exact: true }).click();
        await expect(page.getByRole('button', { name: 'Country code: India +91' })).toBeFocused();
        await page.getByRole('textbox', { name: 'Phone number' }).fill('98765 43210');
        await expect(page.locator('#country-code-modal')).toBeHidden();
        await page.getByRole('button', { name: 'Send code', exact: true }).click();
        await expect(page.getByRole('heading', { name: 'Verify your account' })).toBeVisible();
        expect(await mock.calls('LoginPhoneNumber')).toMatchObject([{ args: ['+919876543210'] }]);
    });
}

test('country search handles keyboard selection, international paste, and mobile Back', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 700 });
    await page.addInitScript(() => history.replaceState(null, '', '/?mobile=android'));
    const mock = await bootTDrive(page, { CheckLoginStatus: resolves(false), LoginPhoneNumber: resolves(null) });
    const picker = page.locator('#telegram-country');
    await picker.click();
    expect(await page.evaluate(() => window.__tdriveHandleBack?.())).toBe(true);
    await expect(picker).toHaveAttribute('aria-expanded', 'false');
    await picker.click();
    await page.getByRole('searchbox').fill('India');
    await page.getByRole('button', { name: 'India +91', exact: true }).focus();
    await page.keyboard.press('Enter');
    await page.getByRole('textbox', { name: 'Phone number' }).fill('+1 (416) 555-0123');
    await expect(picker).toHaveAccessibleName('Country code: Canada +1');
    await page.getByRole('button', { name: 'Send code', exact: true }).click();
    await expect(page.getByRole('heading', { name: 'Verify your account' })).toBeVisible();
    expect(await mock.calls('LoginPhoneNumber')).toMatchObject([{ args: ['+14165550123'] }]);
});
