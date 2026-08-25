import { get } from 'svelte/store';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { MountStatusView } from '../../types';
import {
    createMountController,
    type MountApi,
    type MountNotice,
} from './mount-controller';
import { mountSelection } from './mount-selection-store';

interface Deferred<T> {
    promise: Promise<T>;
    resolve(value: T): void;
    reject(reason: unknown): void;
}

function deferred<T>(): Deferred<T> {
    let resolve!: (value: T) => void;
    let reject!: (reason: unknown) => void;
    const promise = new Promise<T>((accept, decline) => {
        resolve = accept;
        reject = decline;
    });
    return { promise, resolve, reject };
}

function status(overrides: Partial<MountStatusView> = {}): MountStatusView {
    return {
        phase: 'idle',
        mounted: false,
        mode: 'read-only',
        writeState: 'disabled',
        acceptingWrites: false,
        activeWrites: 0,
        label: 'Tdrive personal',
        location: '',
        error: '',
        drive: null,
        ...overrides,
    };
}

function mountedStatus(overrides: Partial<MountStatusView> = {}): MountStatusView {
    return status({
        phase: 'mounted',
        mounted: true,
        location: '/Volumes/Tdrive personal',
        drive: { id: 42, title: 'Personal', kind: 'personal' },
        ...overrides,
    });
}

function writableMountedStatus(overrides: Partial<MountStatusView> = {}): MountStatusView {
    return mountedStatus({
        mode: 'read-write',
        writeState: 'ready',
        acceptingWrites: true,
        ...overrides,
    });
}

function api(overrides: Partial<MountApi> = {}): MountApi {
    return {
        mountDrive: vi.fn(async () => mountedStatus()),
        mountStatus: vi.fn(async () => status()),
        unmountDrive: vi.fn(async () => status()),
        unlockEncryption: vi.fn(async () => false),
        ...overrides,
    };
}

afterEach(() => {
    mountSelection.reset();
});

describe('mount controller', () => {
    it('loads and mounts the only available drive through the controller', async () => {
        const mountDrives = vi.fn(async () => mountedStatus());
        const loadDrives = vi.fn(async () => [
            { id: 42, title: 'Personal', kind: 'personal' as const },
        ]);
        const onAction = vi.fn();
        const controller = createMountController(api({ mountDrives }));

        await controller.requestMount({ loadDrives, onAction });

        expect(loadDrives).toHaveBeenCalledTimes(1);
        expect(onAction).toHaveBeenCalledTimes(1);
        expect(mountDrives).toHaveBeenCalledWith([42]);
        expect(get(controller.loadingDrives)).toBe(false);
    });

    it('opens the picker from one coalesced drive-list request', async () => {
        const pending = deferred<readonly [
            { id: number; title: string; kind: 'personal' },
            { id: number; title: string; kind: 'shared' },
        ]>();
        const loadDrives = vi.fn(() => pending.promise);
        const controller = createMountController(api());

        const first = controller.requestMount({ loadDrives });
        const duplicate = controller.requestMount({ loadDrives });

        expect(duplicate).toBe(first);
        expect(get(controller.loadingDrives)).toBe(true);
        pending.resolve([
            { id: 10, title: 'Personal', kind: 'personal' },
            { id: 20, title: 'Project', kind: 'shared' },
        ]);
        await first;

        expect(loadDrives).toHaveBeenCalledTimes(1);
        expect(get(controller.loadingDrives)).toBe(false);
        expect(get(mountSelection.state)).toMatchObject({
            open: true,
            selectedIds: [10, 20],
        });
    });

    it('mounts a sanitized immutable drive selection and retries that selection after unlock', async () => {
        const selected = [7, 7, -1, 9];
        const mountDrives = vi.fn()
            .mockRejectedValueOnce(new Error('mount controller: encryption password required'))
            .mockResolvedValueOnce(writableMountedStatus({ label: 'TDrive' }));
        const unlockEncryption = vi.fn(async () => true);
        const controller = createMountController(api({ mountDrives, unlockEncryption }));

        await controller.mount(selected);

        expect(selected).toEqual([7, 7, -1, 9]);
        expect(mountDrives).toHaveBeenNthCalledWith(1, [7, 9]);
        expect(mountDrives).toHaveBeenNthCalledWith(2, [7, 9]);
        expect(unlockEncryption).toHaveBeenCalledTimes(1);
        expect(get(controller)).toMatchObject({ mounted: true, label: 'TDrive' });
    });

    it('prompts once and retries the same encrypted mount after unlock', async () => {
        const mountDrive = vi
            .fn<MountApi['mountDrive']>()
            .mockRejectedValueOnce(new Error('mount controller: encryption password required'))
            .mockResolvedValueOnce(writableMountedStatus());
        const unlockEncryption = vi.fn(async () => true);
        const controller = createMountController(api({ mountDrive, unlockEncryption }));

        await controller.mount();

        expect(unlockEncryption).toHaveBeenCalledTimes(1);
        expect(mountDrive).toHaveBeenCalledTimes(2);
        expect(get(controller)).toEqual(writableMountedStatus());
    });

    it('returns to Mount without a retry or error when encryption unlock is cancelled', async () => {
        const mountDrive = vi.fn<MountApi['mountDrive']>()
            .mockRejectedValue(new Error('encryption password required'));
        const unlockEncryption = vi.fn(async () => false);
        const notices: MountNotice[] = [];
        const controller = createMountController(
            api({ mountDrive, unlockEncryption }),
            (notice) => notices.push(notice),
        );

        const first = controller.mount();
        const duplicate = controller.mount();
        await first;

        expect(duplicate).toBe(first);
        expect(unlockEncryption).toHaveBeenCalledTimes(1);
        expect(mountDrive).toHaveBeenCalledTimes(1);
        expect(get(controller)).toMatchObject({ phase: 'idle', mounted: false, error: '' });
        expect(notices[notices.length - 1]).toMatchObject({ id: 'mount-drive', level: 'info', spinner: false });
    });

    it('does not prompt for unrelated mount failures', async () => {
        const unlockEncryption = vi.fn(async () => true);
        const controller = createMountController(api({
            mountDrive: vi.fn(async () => { throw new Error('drive letter unavailable'); }),
            unlockEncryption,
        }));

        await controller.mount();

        expect(unlockEncryption).not.toHaveBeenCalled();
        expect(get(controller)).toMatchObject({ phase: 'error', mounted: false });
    });

    it('preserves an honest writable status from the backend', async () => {
        const result = writableMountedStatus({ activeWrites: 1 });
        const controller = createMountController(api({ mountDrive: vi.fn(async () => result) }));

        await controller.mount();

        expect(get(controller)).toEqual(result);
    });
    it('coalesces mount requests and pins the mounted drive', async () => {
        const pending = deferred<MountStatusView>();
        const mountDrive = vi.fn(() => pending.promise);
        const notices: MountNotice[] = [];
        const controller = createMountController(
            api({ mountDrive }),
            (notice) => notices.push(notice),
        );

        const first = controller.mount();
        const duplicate = controller.mount();

        expect(first).toBe(duplicate);
        expect(mountDrive).toHaveBeenCalledTimes(1);
        expect(get(controller)).toMatchObject({ phase: 'mounting', mounted: false });

        const result = mountedStatus();
        pending.resolve(result);
        await first;

        expect(get(controller)).toEqual(result);
        expect(notices[notices.length - 1]).toMatchObject({
            id: 'mount-drive',
            level: 'success',
            title: 'Tdrive personal mounted',
        });

        await controller.mount();
        expect(mountDrive).toHaveBeenCalledTimes(1);
        expect(get(controller).drive).toEqual({ id: 42, title: 'Personal', kind: 'personal' });
    });

    it('keeps the pinned drive visible until disconnect completes', async () => {
        const pending = deferred<MountStatusView>();
        const unmountDrive = vi.fn(() => pending.promise);
        const notices: MountNotice[] = [];
        const controller = createMountController(
            api({
                unmountDrive,
                mountStatus: vi.fn(async () => mountedStatus()),
            }),
            (notice) => notices.push(notice),
        );
        await controller.refresh();

        const first = controller.disconnect();
        const duplicate = controller.disconnect();

        expect(first).toBe(duplicate);
        expect(unmountDrive).toHaveBeenCalledTimes(1);
        expect(get(controller)).toMatchObject({
            phase: 'disconnecting',
            mounted: true,
            drive: { id: 42, title: 'Personal' },
        });
        expect(notices[notices.length - 1]).toMatchObject({
            level: 'info',
            title: 'Ejecting Tdrive...',
        });

        pending.resolve(status());
        await first;

        expect(get(controller)).toEqual(status());
        expect(notices[notices.length - 1]).toMatchObject({
            level: 'success',
            title: 'Tdrive ejected',
        });
    });

    it('surfaces a safe error and permits a clean retry', async () => {
        const mountDrive = vi
            .fn<MountApi['mountDrive']>()
            .mockRejectedValueOnce(new Error('GET http://127.0.0.1:1234/tdrive-deadbeef failed'))
            .mockResolvedValueOnce(mountedStatus());
        const notices: MountNotice[] = [];
        const controller = createMountController(
            api({ mountDrive }),
            (notice) => notices.push(notice),
        );

        await controller.mount();

        expect(get(controller)).toMatchObject({
            phase: 'error',
            mounted: false,
            error: 'The drive could not be mounted. Try again.',
        });
        expect(JSON.stringify(notices)).not.toContain('127.0.0.1');

        await controller.mount();

        expect(mountDrive).toHaveBeenCalledTimes(2);
        expect(get(controller)).toEqual(mountedStatus());
    });

    it('coalesces status refreshes without replacing an active operation', async () => {
        const pending = deferred<MountStatusView>();
        const mountStatus = vi.fn(() => pending.promise);
        const controller = createMountController(api({ mountStatus }));

        const first = controller.refresh();
        const duplicate = controller.refresh();

        expect(first).toBe(duplicate);
        expect(mountStatus).toHaveBeenCalledTimes(1);

        pending.resolve(mountedStatus());
        await first;

        expect(get(controller)).toEqual(mountedStatus());
    });

    it('discards a stale status response after a newer mount completes', async () => {
        const staleStatus = deferred<MountStatusView>();
        const controller = createMountController(api({
            mountStatus: vi.fn(() => staleStatus.promise),
            mountDrive: vi.fn(async () => mountedStatus()),
        }));

        const refresh = controller.refresh();
        await controller.mount();
        staleStatus.resolve(status());
        await refresh;

        expect(get(controller)).toEqual(mountedStatus());
    });

    it('preserves the pinned drive when disconnect fails', async () => {
        const notices: MountNotice[] = [];
        const controller = createMountController(
            api({
                mountStatus: vi.fn(async () => mountedStatus()),
                unmountDrive: vi.fn(async () => {
                    throw new Error('diskutil could not disconnect the volume');
                }),
            }),
            (notice) => notices.push(notice),
        );
        await controller.refresh();

        await controller.disconnect();

        expect(get(controller)).toMatchObject({
            phase: 'error',
            mounted: true,
            drive: { id: 42, title: 'Personal' },
            error: 'diskutil could not eject the volume',
        });
        expect(notices[notices.length - 1]).toMatchObject({
            level: 'error',
            title: 'Could not eject Tdrive',
            body: 'diskutil could not eject the volume',
        });
        expect(JSON.stringify(notices)).not.toMatch(/disconnect/i);
    });

    it('resynchronizes the writable lifecycle after eject fails', async () => {
        const mounted = writableMountedStatus({ activeWrites: 2 });
        const drained = writableMountedStatus({
            writeState: 'drained',
            acceptingWrites: false,
            activeWrites: 0,
        });
        const mountStatus = vi
            .fn<MountApi['mountStatus']>()
            .mockResolvedValueOnce(mounted)
            .mockResolvedValueOnce(drained);
        const controller = createMountController(api({
            mountStatus,
            unmountDrive: vi.fn(async () => {
                throw new Error('The OS still owns the mount');
            }),
        }));
        await controller.refresh();

        await controller.disconnect();

        expect(mountStatus).toHaveBeenCalledTimes(2);
        expect(get(controller)).toMatchObject({
            phase: 'error',
            mounted: true,
            mode: 'read-write',
            writeState: 'drained',
            acceptingWrites: false,
            activeWrites: 0,
            error: 'The OS still owns the mount',
        });
    });

});
