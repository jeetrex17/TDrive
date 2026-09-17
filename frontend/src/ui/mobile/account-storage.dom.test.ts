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
import { handleBackPress } from './mobile-back';
import { activeTab } from './mobile-shell-store';
import { sidebarState } from '../sidebar/sidebar-store';

let host: HTMLElement;
let app: Record<string, unknown> | null = null;

function storageRow(): string {
    return host.querySelector('.account-row-static')?.textContent ?? '';
}

function setDrive(id: number, title: string): void {
    sidebarState.set({
        personal: [{ id, title, kind: 'personal', isActive: true, inviteLink: '' }],
        shared: [],
        pending: [],
        activeChannelId: id,
        photosActive: false,
    });
}

function deferred<T>(): { promise: Promise<T>; resolve: (value: T) => void } {
    let resolve!: (value: T) => void;
    const promise = new Promise<T>((done) => { resolve = done; });
    return { promise, resolve };
}

beforeEach(() => {
    vi.clearAllMocks();
    activeTab.set('account');
    setDrive(1, 'Personal files');
    host = document.createElement('div');
    document.body.append(host);
});

afterEach(async () => {
    if (app) await unmount(app);
    app = null;
    host.remove();
    activeTab.set('files');
    sidebarState.set({ personal: [], shared: [], pending: [], activeChannelId: null, photosActive: false });
});

describe('Account storage row', () => {
    it('restores the Appearance row after its visible back control closes the drill-in', async () => {
        mocks.getStorageUsed.mockResolvedValue(10);
        app = mount(AccountTab, { target: host });
        flushSync();

        const opener = [...host.querySelectorAll<HTMLButtonElement>('.account-row')]
            .find((button) => button.textContent?.includes('Appearance'));
        opener?.click();
        flushSync();
        (host.querySelector<HTMLButtonElement>('.account-appearance-back'))?.click();
        flushSync();

        await vi.waitFor(() => {
            const restoredOpener = [...host.querySelectorAll<HTMLButtonElement>('.account-row')]
                .find((button) => button.textContent?.includes('Appearance'));
            expect(document.activeElement).toBe(restoredOpener);
        });
    });

    it('returns BACK from Appearance before leaving Account and restores its opener focus', async () => {
        mocks.getStorageUsed.mockResolvedValue(10);
        app = mount(AccountTab, { target: host });
        flushSync();

        const opener = [...host.querySelectorAll<HTMLButtonElement>('.account-row')]
            .find((button) => button.textContent?.includes('Appearance'));
        expect(opener).toBeDefined();
        opener?.focus();
        opener?.click();
        flushSync();
        expect(host.querySelector('.account-appearance-detail')).not.toBeNull();

        expect(handleBackPress()).toBe(true);
        await Promise.resolve();
        flushSync();

        expect(host.querySelector('.account-appearance-detail')).toBeNull();
        await vi.waitFor(() => {
            const restoredOpener = [...host.querySelectorAll<HTMLButtonElement>('.account-row')]
                .find((button) => button.textContent?.includes('Appearance'));
            expect(document.activeElement).toBe(restoredOpener);
        });
    });

    it('closes the Appearance drill-in when another tab becomes active', async () => {
        mocks.getStorageUsed.mockResolvedValue(10);
        app = mount(AccountTab, { target: host });
        flushSync();

        const opener = [...host.querySelectorAll<HTMLButtonElement>('.account-row')]
            .find((button) => button.textContent?.includes('Appearance'));
        opener?.click();
        flushSync();
        expect(host.querySelector('.account-appearance-detail')).not.toBeNull();

        activeTab.set('files');
        flushSync();

        expect(host.querySelector('.account-appearance-detail')).toBeNull();
    });

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

    it('does not publish an old drive figure after the active drive changes', async () => {
        const first = deferred<number>();
        const second = deferred<number>();
        mocks.getStorageUsed.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);
        app = mount(AccountTab, { target: host });
        flushSync();

        expect(storageRow()).toContain('Personal files');
        setDrive(2, 'Project archive');
        flushSync();
        expect(storageRow()).toContain('Project archive');

        first.resolve(1500);
        await Promise.resolve();
        flushSync();
        expect(storageRow()).not.toContain('1.5 KB');

        second.resolve(2000);
        await vi.waitFor(() => expect(storageRow()).toContain('2 KB'));
    });
});
