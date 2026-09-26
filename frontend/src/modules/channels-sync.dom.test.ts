import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { get } from 'svelte/store';

const api = vi.hoisted(() => ({
    syncChannel: vi.fn(),
}));

vi.mock('../api', () => ({
    approveJoinRequest: vi.fn(),
    checkPendingJoin: vi.fn(),
    createSharedDrive: vi.fn(),
    getApprovalInviteLink: vi.fn(),
    getInviteLink: vi.fn(),
    isMobilePlatform: () => true,
    joinSharedDrive: vi.fn(),
    leaveSharedDrive: vi.fn(),
    listChannels: vi.fn(),
    listJoinRequests: vi.fn(),
    listPendingJoins: vi.fn(),
    onRuntimeEvent: vi.fn(),
    rejectJoinRequest: vi.fn(),
    removePendingJoin: vi.fn(),
    runtimeEventsAvailable: () => false,
    setActiveChannel: vi.fn(),
    syncChannel: api.syncChannel,
}));

vi.mock('./search', () => ({ runGlobalSearch: vi.fn() }));
vi.mock('./renditions/runtime', () => ({ resetRenditions: vi.fn() }));

import { state } from '../state';
import { driveSyncStatus } from '../ui/mobile/mobile-shell-store';
import { bindChannelsRenderers, refreshActiveDrive } from './channels';

function deferred<T>() {
    let resolve!: (value: T | PromiseLike<T>) => void;
    let reject!: (reason?: unknown) => void;
    const promise = new Promise<T>((res, rej) => {
        resolve = res;
        reject = rej;
    });
    return { promise, resolve, reject };
}

const refreshFiles = vi.fn();

beforeEach(() => {
    vi.clearAllMocks();
    state.activeChannel = { id: 1, title: 'Drive A', kind: 'personal' };
    driveSyncStatus.set('idle');
    bindChannelsRenderers({ onSidebarUpdate: vi.fn(), onActiveDriveChanged: refreshFiles });
});

afterEach(() => {
    state.activeChannel = null;
    driveSyncStatus.set('idle');
});

describe('refreshActiveDrive', () => {
    it('does not publish an old drive result after the active drive changes', async () => {
        const sync = deferred<void>();
        api.syncChannel.mockReturnValueOnce(sync.promise);

        const request = refreshActiveDrive();
        expect(get(driveSyncStatus)).toBe('syncing');

        state.activeChannel = { id: 2, title: 'Drive B', kind: 'shared' };
        sync.resolve();
        await request;

        expect(get(driveSyncStatus)).toBe('syncing');
        expect(refreshFiles).not.toHaveBeenCalled();
    });

    it('publishes and refreshes when the same drive is still active', async () => {
        api.syncChannel.mockResolvedValueOnce(undefined);

        await refreshActiveDrive();

        expect(get(driveSyncStatus)).toBe('synced');
        expect(refreshFiles).toHaveBeenCalledTimes(1);
    });

    it('does not publish a stale failure after a drive change', async () => {
        const sync = deferred<void>();
        api.syncChannel.mockReturnValueOnce(sync.promise);

        const request = refreshActiveDrive();
        state.activeChannel = { id: 2, title: 'Drive B', kind: 'shared' };
        sync.reject(new Error('Drive A offline'));
        await request;

        expect(get(driveSyncStatus)).toBe('syncing');
        expect(refreshFiles).not.toHaveBeenCalled();
    });

    it('ignores an older refresh that settles after a newer request on the same drive', async () => {
        const first = deferred<void>();
        const second = deferred<void>();
        api.syncChannel
            .mockReturnValueOnce(first.promise)
            .mockReturnValueOnce(second.promise);

        const olderRequest = refreshActiveDrive();
        const newerRequest = refreshActiveDrive();
        second.resolve();
        await newerRequest;
        expect(get(driveSyncStatus)).toBe('synced');

        first.reject(new Error('old request failed'));
        await olderRequest;

        expect(get(driveSyncStatus)).toBe('synced');
        expect(refreshFiles).toHaveBeenCalledTimes(1);
    });
});
