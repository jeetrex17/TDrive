import { expect } from '@playwright/test';
import { bootTDrive, resolves, test } from './wails-mock';

const source = {
    peer_kind: 'group', peer_id: 44, title: 'Field Team', username: '',
    connected: true, protected: false, available: true, account_id: 7, generation: 'current',
};
const groups = [{ id: 'work', name: 'Work' }];
const assignments = [{ peer_kind: 'group', peer_id: 44, generation: 'current', group_id: 'work' }];

for (const mobile of [false, true]) {
    test(`${mobile ? 'mobile' : 'desktop'} groups a connected source from its actions`, async ({ page }) => {
        if (mobile) await page.setViewportSize({ width: 390, height: 844 });
        const mock = await bootTDrive(page, {
            ListConnectedChannelSources: resolves([source]),
            ListSourceGroups: resolves({ groups: [], assignments: [] }),
            SaveSourceGroup: resolves({ groups, assignments: [] }),
            MoveSourceToGroup: resolves({ groups, assignments }),
            DeleteSourceGroup: resolves({ groups: [], assignments: [] }),
            Haptic: resolves(null),
        }, mobile ? { url: '/?mobile=ios' } : {});
        const nav = page.locator(mobile ? '.drive-switcher-sheet' : '.sidebar');
        const switcher = page.getByRole('button', { name: /Switch drive/ });
        const dialog = page.locator('#source-organization-modal').getByRole('dialog');
        const row = nav.getByRole('button', { name: 'Group Field Team', exact: true });
        const manage = nav.getByRole('button', { name: 'Manage source groups' });

        if (mobile) await switcher.click();
        await manage.click();
        await dialog.getByRole('button', { name: 'New group' }).click();
        await dialog.getByRole('textbox', { name: 'Group name' }).fill('Work');
        await dialog.getByRole('button', { name: 'Create group' }).click();
        await expect(dialog.getByRole('button', { name: 'Rename Work' })).toBeVisible();
        await dialog.getByRole('button', { name: 'Done' }).click();
        await expect(dialog).toBeHidden();

        if (mobile) {
            await switcher.click();
            await nav.getByRole('button', { name: 'Actions for Field Team' }).click();
        } else {
            await row.click({ button: 'right' });
        }
        await page.getByRole(mobile ? 'button' : 'menuitem', { name: 'Move to group…' }).click();
        await dialog.getByRole('button', { name: 'Work', exact: true }).click();
        await expect(dialog).toBeHidden();
        expect((await mock.calls('MoveSourceToGroup')).map(({ args }) => args)).toEqual([['group', 44, 7, 'current', 'work']]);

        if (mobile) await switcher.click();
        const group = nav.locator('.channel-nav-group');
        await expect(group.getByRole('button', { name: 'Group Field Team', exact: true })).toBeVisible();
        await group.getByRole('button', { name: 'Work', exact: true }).click();
        await expect(row).toBeHidden();
        await group.getByRole('button', { name: 'Work', exact: true }).click();
        await expect(row).toBeVisible();

        if (!mobile) {
            const updatedGroups = [{ id: 'work', name: 'Projects' }];
            await mock.setPlan('SaveSourceGroup', resolves({ groups: updatedGroups, assignments }));
            await manage.click();
            await dialog.getByRole('button', { name: 'Rename Work' }).click();
            await dialog.getByRole('textbox', { name: 'Group name' }).fill('Projects');
            await dialog.getByRole('button', { name: 'Save', exact: true }).click();
            await dialog.getByRole('button', { name: 'Delete Projects' }).click();
            await dialog.getByRole('button', { name: 'Delete group' }).click();
            await expect(dialog.getByText('No groups yet.')).toBeVisible();
            await dialog.getByRole('button', { name: 'Done' }).click();
            await expect(nav.locator('.channel-nav-group')).toHaveCount(0);
            await expect(row).toBeVisible();
        }
    });
}

for (const expanded of [false, true]) {
    test(`desktop restores focus after moving a source to a ${expanded ? 'visible' : 'collapsed'} group`, async ({ page }) => {
        await bootTDrive(page, {
            ListConnectedChannelSources: resolves([source]),
            ListSourceGroups: resolves({ groups, assignments: [] }),
            MoveSourceToGroup: resolves({ groups, assignments }),
        });
        const nav = page.locator('.sidebar');
        const group = nav.getByRole('button', { name: 'Work', exact: true });
        const row = nav.getByRole('button', { name: 'Group Field Team', exact: true });
        const dialog = page.getByRole('dialog', { name: 'Move to group', exact: true });
        if (!expanded) await group.click();

        await row.press('Shift+F10');
        await page.getByRole('menuitem', { name: 'Move to group…' }).press('Enter');
        await dialog.getByRole('button', { name: 'Work', exact: true }).press('Enter');

        await expect(dialog).toBeHidden();
        await expect(expanded ? row : group).toBeFocused();
    });
}
