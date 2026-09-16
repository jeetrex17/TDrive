// The phone had no way to see how much of a drive is in use: the element the
// file list writes the figure into is one of the shell's hidden hosts, so the
// number was computed and then thrown away. The Account tab asks for it itself.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';

const mocks = vi.hoisted(() => ({ getStorageUsed: vi.fn() }));

vi.mock('../../api', () => ({
    getStorageUsed: mocks.getStorageUsed,
    isMobilePlatform: () => true,
}));
vi.mock('../../modules/channels', () => ({ switchActiveChannel: vi.fn() }));
vi.mock('../../modules/modals/encryption-settings', () => ({ openEncryptionSettingsModal: vi.fn() }));
vi.mock('../../modules/modals/join-drive', () => ({ openJoinDriveModal: vi.fn() }));
vi.mock('../../modules/modals/new-drive', () => ({ openNewDriveModal: vi.fn() }));
vi.mock('../../modules/modals/logout', () => ({ openLogoutModal: vi.fn() }));
vi.mock('../theme/AppearancePanel.svelte', () => ({ default: function noop() {} }));
vi.mock('../chrome/Avatar.svelte', () => ({ default: function noop() {} }));

import AccountTab from './AccountTab.svelte';
import { activeTab } from './mobile-shell-store';

let host: HTMLElement;
let app: Record<string, unknown> | null = null;

function storageRow(): string {
    return host.querySelector('.account-row-static')?.textContent ?? '';
}

beforeEach(() => {
    vi.clearAllMocks();
    activeTab.set('account');
    host = document.createElement('div');
    document.body.append(host);
});

afterEach(async () => {
    if (app) await unmount(app);
    app = null;
    host.remove();
    activeTab.set('files');
});

describe('Account storage row', () => {
    it('reports what the drive is using', async () => {
        mocks.getStorageUsed.mockResolvedValue(1500);
        app = mount(AccountTab, { target: host });
        flushSync();
        expect(storageRow()).toContain('Calculating');

        await vi.waitFor(() => expect(storageRow()).toContain('1.5 KB'));
        expect(mocks.getStorageUsed).toHaveBeenCalledTimes(1);
    });

    it('says so rather than showing a wrong figure when the drive will not answer', async () => {
        mocks.getStorageUsed.mockRejectedValue(new Error('backend not ready'));
        app = mount(AccountTab, { target: host });
        flushSync();

        await vi.waitFor(() => expect(storageRow()).toContain('Unavailable'));
    });

    it('does not ask while the tab is not being looked at', async () => {
        activeTab.set('files');
        mocks.getStorageUsed.mockResolvedValue(10);
        app = mount(AccountTab, { target: host });
        flushSync();
        expect(mocks.getStorageUsed).not.toHaveBeenCalled();

        activeTab.set('account');
        flushSync();
        await vi.waitFor(() => expect(mocks.getStorageUsed).toHaveBeenCalledTimes(1));
    });
});
