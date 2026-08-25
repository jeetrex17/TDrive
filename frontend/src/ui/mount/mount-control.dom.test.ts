import { get } from 'svelte/store';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, tick, unmount } from 'svelte';
import type { MountStatusView } from '../../types';
import MountControl from './MountControl.svelte';
import { createMountController, type MountApi } from './mount-controller';
import { mountSelection } from './mount-selection-store';

interface Deferred<T> {
    promise: Promise<T>;
    resolve(value: T): void;
}

function deferred<T>(): Deferred<T> {
    let resolve!: (value: T) => void;
    const promise = new Promise<T>((accept) => {
        resolve = accept;
    });
    return { promise, resolve };
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
        label: 'Tdrive personal',
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

let host: HTMLElement | null = null;
let component: Record<string, unknown> | null = null;

async function settle(): Promise<void> {
    await Promise.resolve();
    await tick();
    flushSync();
}

function click(selector: string): void {
    const button = host?.querySelector<HTMLButtonElement>(selector);
    if (!button) throw new Error(`missing ${selector}`);
    button.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    flushSync();
}

afterEach(async () => {
    if (component) await unmount(component);
    host?.remove();
    component = null;
    host = null;
    mountSelection.reset();
});

describe('MountControl', () => {
    it('mounts the only known drive directly', async () => {
        const mountDrives = vi.fn(async () => mountedStatus());
        const controller = createMountController({
            mountDrive: vi.fn(async () => mountedStatus()),
            mountDrives,
            mountStatus: vi.fn(async () => status()),
            unmountDrive: vi.fn(async () => status()),
        });
        host = document.createElement('div');
        document.body.appendChild(host);
        component = mount(MountControl, {
            target: host,
            props: {
                controller,
                loadDrives: async () => [{ id: 42, title: 'Personal', kind: 'personal' }],
            },
        });
        await settle();

        click('#mount-drive-button');
        await settle();

        expect(mountDrives).toHaveBeenCalledWith([42]);
        expect(get(mountSelection.state).open).toBe(false);
    });

    it('opens an all-selected picker instead of mounting immediately when multiple drives exist', async () => {
        const mountDrives = vi.fn(async () => mountedStatus());
        const controller = createMountController({
            mountDrive: vi.fn(async () => mountedStatus()),
            mountDrives,
            mountStatus: vi.fn(async () => status()),
            unmountDrive: vi.fn(async () => status()),
        });
        const onMenuAction = vi.fn();
        host = document.createElement('div');
        document.body.appendChild(host);
        component = mount(MountControl, {
            target: host,
            props: {
                controller,
                onMenuAction,
                loadDrives: async () => [
                    { id: 11, title: 'Personal', kind: 'personal' },
                    { id: 22, title: 'Project', kind: 'shared' },
                ],
            },
        });
        await settle();

        click('#mount-drive-button');
        await settle();

        expect(mountDrives).not.toHaveBeenCalled();
        expect(onMenuAction).toHaveBeenCalledTimes(1);
        expect(get(mountSelection.state)).toMatchObject({
            open: true,
            selectedIds: [11, 22],
        });

        mountSelection.confirm();
        await settle();
        expect(mountDrives).toHaveBeenCalledWith([11, 22]);
    });

    it('keeps the eject action single-line after eject fails', async () => {
        const api: MountApi = {
            mountDrive: vi.fn(async () => writableMountedStatus()),
            mountStatus: vi.fn(async () => writableMountedStatus()),
            unmountDrive: vi.fn(async () => {
                throw new Error('pending commits could not finish');
            }),
        };
        const controller = createMountController(api);
        host = document.createElement('div');
        document.body.appendChild(host);
        component = mount(MountControl, { target: host, props: { controller } });
        await settle();

        click('#disconnect-mounted-drive-button');
        await settle();

        expect(host.textContent).not.toContain('Writes paused');
        const eject = host.querySelector<HTMLButtonElement>('#disconnect-mounted-drive-button');
        expect(eject?.textContent).toContain('Eject Tdrive');
        expect(eject?.textContent).not.toContain('Retry');
    });

    it('hides the writable mode while preserving the active-write drain state', async () => {
        const pending = deferred<MountStatusView>();
        const api: MountApi = {
            mountDrive: vi.fn(async () => writableMountedStatus({ activeWrites: 2 })),
            mountStatus: vi.fn(async () => writableMountedStatus({ activeWrites: 2 })),
            unmountDrive: vi.fn(() => pending.promise),
        };
        const controller = createMountController(api);
        host = document.createElement('div');
        document.body.appendChild(host);
        component = mount(MountControl, { target: host, props: { controller } });
        await settle();

        expect(host.textContent).not.toContain('Read/write');
        click('#disconnect-mounted-drive-button');
        await settle();
        expect(host.textContent).toContain('Finishing 2 changes...');

        pending.resolve(status());
        await settle();
    });
    it('renders a full-width menu item with accessible menu semantics', async () => {
        const api: MountApi = {
            mountDrive: vi.fn(async () => mountedStatus()),
            mountStatus: vi.fn(async () => status()),
            unmountDrive: vi.fn(async () => status()),
        };
        const controller = createMountController(api);
        host = document.createElement('div');
        document.body.appendChild(host);
        component = mount(MountControl, {
            target: host,
            props: { controller },
        });
        await settle();

        const group = host.querySelector('.mount-control');
        const mountButton = host.querySelector<HTMLButtonElement>('#mount-drive-button');
        expect(group?.getAttribute('role')).toBe('group');
        expect(group?.getAttribute('aria-label')).toBe('Tdrive personal mount controls');
        expect(mountButton?.getAttribute('role')).toBe('menuitem');
        expect(mountButton?.getAttribute('aria-label')).toBe('Mount Tdrive personal');
        expect(mountButton?.textContent?.trim()).toBe('Mount');

        click('#mount-drive-button');
        await settle();

        expect(api.mountDrive).toHaveBeenCalledTimes(1);
        const ejectButton = host.querySelector<HTMLButtonElement>('#disconnect-mounted-drive-button');
        expect(ejectButton?.getAttribute('role')).toBe('menuitem');
        expect(ejectButton?.getAttribute('aria-label')).toBe('Eject Tdrive');
        expect(ejectButton?.textContent?.trim()).toBe('Eject Tdrive');
    });

    it('notifies the owning menu when a menu action starts', async () => {
        const onMenuAction = vi.fn();
        const api: MountApi = {
            mountDrive: vi.fn(async () => mountedStatus()),
            mountStatus: vi.fn(async () => status()),
            unmountDrive: vi.fn(async () => status()),
        };
        const controller = createMountController(api);
        host = document.createElement('div');
        document.body.appendChild(host);
        component = mount(MountControl, {
            target: host,
            props: { controller, onMenuAction },
        });
        await settle();

        click('#mount-drive-button');
        await settle();

        expect(onMenuAction).toHaveBeenCalledTimes(1);
        expect(api.mountDrive).toHaveBeenCalledTimes(1);
    });

    it('ejects a mounted menu drive exactly once and preserves its busy state', async () => {
        const pending = deferred<MountStatusView>();
        const onMenuAction = vi.fn();
        const unmountDrive = vi.fn(() => pending.promise);
        const api: MountApi = {
            mountDrive: vi.fn(async () => mountedStatus()),
            mountStatus: vi.fn(async () => mountedStatus()),
            unmountDrive,
        };
        const controller = createMountController(api);
        host = document.createElement('div');
        document.body.appendChild(host);
        component = mount(MountControl, {
            target: host,
            props: { controller, onMenuAction },
        });
        await settle();

        click('#disconnect-mounted-drive-button');

        const ejectButton = host.querySelector<HTMLButtonElement>('#disconnect-mounted-drive-button');
        expect(unmountDrive).toHaveBeenCalledTimes(1);
        expect(onMenuAction).toHaveBeenCalledTimes(1);
        expect(ejectButton?.disabled).toBe(true);
        expect(ejectButton?.getAttribute('aria-busy')).toBe('true');
        expect(ejectButton?.textContent).toContain('Ejecting Tdrive...');
        expect(ejectButton?.textContent).not.toContain('Disconnect');

        ejectButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }));
        flushSync();
        expect(unmountDrive).toHaveBeenCalledTimes(1);

        pending.resolve(status());
        await settle();
    });

    it('moves from Mount to Mounting... and exposes only the mounted eject action', async () => {
        const pending = deferred<MountStatusView>();
        const api: MountApi = {
            mountDrive: vi.fn(() => pending.promise),
            mountStatus: vi.fn(async () => status()),
            unmountDrive: vi.fn(async () => status()),
        };
        const controller = createMountController(api);
        host = document.createElement('div');
        document.body.appendChild(host);
        component = mount(MountControl, { target: host, props: { controller } });
        await settle();

        expect(host.querySelector('#mount-drive-button')?.textContent).toContain('Mount');
        click('#mount-drive-button');

        const mounting = host.querySelector<HTMLButtonElement>('#mount-drive-button');
        expect(mounting?.textContent).toContain('Mounting Tdrive personal...');
        expect(mounting?.disabled).toBe(true);
        expect(mounting?.getAttribute('aria-busy')).toBe('true');

        pending.resolve(mountedStatus());
        await settle();

        const ejectButton = host.querySelector<HTMLButtonElement>('#disconnect-mounted-drive-button');
        expect(ejectButton?.getAttribute('aria-label')).toBe('Eject Tdrive');
        expect(ejectButton?.textContent?.trim()).toBe('Eject Tdrive');
    });

    it('shows Ejecting Tdrive... while the eject is pending', async () => {
        const pending = deferred<MountStatusView>();
        const api: MountApi = {
            mountDrive: vi.fn(async () => mountedStatus()),
            mountStatus: vi.fn(async () => mountedStatus()),
            unmountDrive: vi.fn(() => pending.promise),
        };
        const controller = createMountController(api);
        host = document.createElement('div');
        document.body.appendChild(host);
        component = mount(MountControl, { target: host, props: { controller } });
        await settle();

        click('#disconnect-mounted-drive-button');

        expect(host.textContent).toContain('Ejecting Tdrive...');
        const ejectButton = host.querySelector<HTMLButtonElement>('#disconnect-mounted-drive-button');
        expect(ejectButton?.getAttribute('aria-label')).toBe('Eject Tdrive');
        expect(ejectButton?.disabled).toBe(true);
        expect(ejectButton?.getAttribute('aria-busy')).toBe('true');

        pending.resolve(status());
        await settle();

        expect(host.querySelector('#mount-drive-button')?.textContent).toContain('Mount');
    });

    it('keeps the Mount label stable after a safe mount failure', async () => {
        const mountDrive = vi
            .fn<MountApi['mountDrive']>()
            .mockRejectedValueOnce(new Error('mount_webdav http://127.0.0.1:7777/tdrive-feedface'))
            .mockResolvedValueOnce(mountedStatus());
        const api: MountApi = {
            mountDrive,
            mountStatus: vi.fn(async () => status()),
            unmountDrive: vi.fn(async () => status()),
        };
        const controller = createMountController(api);
        host = document.createElement('div');
        document.body.appendChild(host);
        component = mount(MountControl, { target: host, props: { controller } });
        await settle();

        click('#mount-drive-button');
        await settle();

        expect(host.querySelector('#mount-drive-button')?.textContent?.trim()).toBe('Mount');
        expect(host.querySelector('#mount-drive-button')?.textContent).not.toContain('Retry');
        expect(host.textContent).not.toContain('127.0.0.1');
        expect(host.querySelector('[role="alert"]')?.textContent).toContain('could not be mounted');

        click('#mount-drive-button');
        await settle();

        expect(mountDrive).toHaveBeenCalledTimes(2);
        expect(host.querySelector('#disconnect-mounted-drive-button')?.textContent?.trim()).toBe('Eject Tdrive');
    });

    it('keeps the menu Mount label stable after a mount failure', async () => {
        const api: MountApi = {
            mountDrive: vi.fn(async () => {
                throw new Error('mount failed');
            }),
            mountStatus: vi.fn(async () => status()),
            unmountDrive: vi.fn(async () => status()),
        };
        const controller = createMountController(api);
        host = document.createElement('div');
        document.body.appendChild(host);
        component = mount(MountControl, {
            target: host,
            props: { controller },
        });
        await settle();

        click('#mount-drive-button');
        await settle();

        const mountButton = host.querySelector<HTMLButtonElement>('#mount-drive-button');
        expect(mountButton?.textContent?.trim()).toBe('Mount');
        expect(mountButton?.textContent).not.toContain('Retry');
        expect(mountButton?.getAttribute('aria-label')).toBe('Mount Tdrive personal');
    });
});
