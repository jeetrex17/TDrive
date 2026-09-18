import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, tick, unmount } from 'svelte';
import ContextMenu from '../menus/ContextMenu.svelte';
import { hideContextMenu, showContextMenu } from '../menus/context-menu-store';
import DriveList from './DriveList.svelte';
import {
    setSidebarVirtualView,
    setSidebarState,
    type SidebarActionMenuRequest,
} from './sidebar-store';

let driveList: Record<string, unknown> | null = null;
let contextMenu: Record<string, unknown> | null = null;
let driveHost: HTMLElement | null = null;
let menuHost: HTMLElement | null = null;

function rect(left: number, top: number, width: number, height: number): DOMRect {
    return {
        x: left,
        y: top,
        left,
        top,
        right: left + width,
        bottom: top + height,
        width,
        height,
        toJSON: () => ({}),
    };
}

async function settle(): Promise<void> {
    flushSync();
    await tick();
    await Promise.resolve();
    await tick();
    flushSync();
}

function mountSharedDriveList(
    onDriveActions: (request: SidebarActionMenuRequest) => void = vi.fn(),
    onPendingActions: (request: SidebarActionMenuRequest) => void = vi.fn(),
): void {
    if (!driveHost) throw new Error('drive host missing');
    driveList = mount(DriveList, {
        target: driveHost,
        props: {
            kind: 'shared',
            onDriveClick: vi.fn(),
            onDriveActions,
            onPendingClick: vi.fn(),
            onPendingActions,
        },
    });
    flushSync();
}

beforeEach(() => {
    setSidebarState({
        personal: [],
        shared: [{
            id: 42,
            title: 'Family archive',
            kind: 'shared',
            isActive: true,
            inviteLink: '',
        }],
        pending: [{
            inviteHash: 'invite-7',
            inviteLink: '',
            title: 'Project records',
            requestedAt: 0,
            lastCheckedAt: 0,
            status: 'pending',
            lastError: '',
        }],
        activeChannelId: 42,
        virtualView: null,
    });

    driveHost = document.createElement('div');
    menuHost = document.createElement('div');
    menuHost.id = 'context-menu';
    document.body.append(driveHost, menuHost);
    contextMenu = mount(ContextMenu, { target: menuHost, props: {} });
});

afterEach(async () => {
    hideContextMenu();
    flushSync();
    if (driveList) await unmount(driveList);
    if (contextMenu) await unmount(contextMenu);
    driveHost?.remove();
    menuHost?.remove();
    driveList = null;
    contextMenu = null;
    driveHost = null;
    menuHost = null;
    setSidebarState({
        personal: [],
        shared: [],
        pending: [],
        activeChannelId: null,
        virtualView: null,
    });
});

describe('DriveList actions', () => {
    it('renders sibling row controls with discoverable labels and current-drive semantics', () => {
        mountSharedDriveList();

        const currentDrive = driveHost?.querySelector<HTMLButtonElement>('[data-channel-id="42"]');
        const actionLabels = Array.from(
            driveHost?.querySelectorAll<HTMLButtonElement>('.drive-actions-trigger') ?? [],
            (button) => button.getAttribute('aria-label'),
        );

        expect(currentDrive?.getAttribute('aria-current')).toBe('page');
        expect(actionLabels).toEqual(['Actions for Family archive', 'Actions for Project records']);
        expect(driveHost?.querySelector('button button')).toBeNull();

        setSidebarVirtualView('photos');
        flushSync();
        expect(currentDrive?.hasAttribute('aria-current')).toBe(false);
    });

    it('opens the existing menu from the overflow button and restores focus on Escape', async () => {
        let requestedPosition: SidebarActionMenuRequest | null = null;
        mountSharedDriveList((request) => {
            requestedPosition = request;
            showContextMenu(request.x, request.y, [
                { label: 'Copy invite link', action: vi.fn() },
                { label: 'Leave drive', action: vi.fn() },
            ]);
        });

        const trigger = driveHost?.querySelector<HTMLButtonElement>('[aria-label="Actions for Family archive"]');
        if (!trigger) throw new Error('drive action trigger missing');
        vi.spyOn(trigger, 'getBoundingClientRect').mockReturnValue(rect(48, 20, 32, 32));

        trigger.focus();
        trigger.click();
        await settle();

        expect(requestedPosition).toEqual({ x: 48, y: 56 });
        expect(trigger.getAttribute('aria-expanded')).toBe('true');
        expect(document.activeElement?.getAttribute('role')).toBe('menuitem');

        document.dispatchEvent(new KeyboardEvent('keydown', {
            key: 'Escape',
            bubbles: true,
            cancelable: true,
        }));
        await settle();

        expect(document.querySelector('.context-menu-panel')).toBeNull();
        expect(trigger.getAttribute('aria-expanded')).toBe('false');
        expect(document.activeElement).toBe(trigger);
    });

    it('preserves pointer coordinates when a shared row is opened by right-click', async () => {
        const onDriveActions = vi.fn((request: SidebarActionMenuRequest) => {
            showContextMenu(request.x, request.y, [
                { label: 'Join requests', action: vi.fn() },
            ]);
        });
        mountSharedDriveList(onDriveActions);

        const driveButton = driveHost?.querySelector<HTMLButtonElement>('[data-channel-id="42"]');
        if (!driveButton) throw new Error('shared drive button missing');
        const event = new MouseEvent('contextmenu', {
            bubbles: true,
            cancelable: true,
            button: 2,
            clientX: 91,
            clientY: 117,
        });

        driveButton.dispatchEvent(event);
        await settle();

        expect(event.defaultPrevented).toBe(true);
        expect(onDriveActions).toHaveBeenCalledTimes(1);
        expect(onDriveActions.mock.calls[0]?.[0]).toEqual({ x: 91, y: 117 });
        expect(document.querySelector('.context-menu-panel')).not.toBeNull();
    });
});
