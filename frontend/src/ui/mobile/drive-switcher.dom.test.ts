// The drive switcher is a modal surface: while it is up it owns focus and the
// shell behind it stops answering, and it can be pushed back down by hand.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';
import { get } from 'svelte/store';
import DriveSwitcherSheet from './DriveSwitcherSheet.svelte';
import { sidebarState } from '../sidebar/sidebar-store';
import { closeDriveSwitcher, driveSwitcherOpen, openDriveSwitcher } from './mobile-shell-store';

const actions = vi.hoisted(() => ({ join: vi.fn(), create: vi.fn() }));
vi.mock('../../modules/modals/join-drive', () => ({ openJoinDriveModal: actions.join }));
vi.mock('../../modules/modals/new-drive', () => ({ openNewDriveModal: actions.create }));

let shell: HTMLElement;
let opener: HTMLButtonElement;
let behind: HTMLElement;
let component: Record<string, unknown> | null = null;

function sheet(): HTMLElement {
    return shell.querySelector('.drive-switcher-sheet') as HTMLElement;
}

// timeStamp is what the velocity estimate reads, and synthetic events all carry
// the same one, which would read every drag as an infinitely fast flick.
function pointer(type: string, target: EventTarget, clientY: number, timeStamp = 0): void {
    const event = new PointerEvent(type, {
        pointerId: 1,
        pointerType: 'touch',
        isPrimary: true,
        clientX: 100,
        clientY,
        bubbles: true,
        cancelable: true,
    });
    Object.defineProperty(event, 'timeStamp', { value: timeStamp });
    target.dispatchEvent(event);
}

beforeEach(() => {
    actions.join.mockReset();
    actions.create.mockReset();
    driveSwitcherOpen.set(false);
    // The sheet renders the drive list itself, so a drive in the sidebar store
    // is what puts a row in it.
    sidebarState.set({
        personal: [{ id: 1, title: 'My Drive', kind: 'personal', isActive: true, inviteLink: '' }],
        shared: [],
        pending: [],
        activeChannelId: 1,
        virtualView: null,
    });
    shell = document.createElement('div');
    shell.className = 'mobile-shell';
    behind = document.createElement('div');
    opener = document.createElement('button');
    opener.className = 'drive-header-btn';
    behind.append(opener);
    shell.append(behind);
    document.body.append(shell);
    component = mount(DriveSwitcherSheet, { target: shell });
    flushSync();
});

describe('actions', () => {
    it('closes the switcher before opening the join sheet', () => {
        openDriveSwitcher();
        flushSync();

        (sheet().querySelector('[data-drive-action="join"]') as HTMLButtonElement).click();
        flushSync();

        expect(get(driveSwitcherOpen)).toBe(false);
        expect(actions.join).toHaveBeenCalledOnce();
    });
});

afterEach(() => {
    if (component) unmount(component);
    component = null;
    shell.remove();
    driveSwitcherOpen.set(false);
});

describe('focus', () => {
    it('lands on the first drive and hands focus back on the way out', () => {
        opener.focus();
        openDriveSwitcher();
        flushSync();

        expect(document.activeElement).toBe(shell.querySelector('.drive-item'));

        closeDriveSwitcher();
        flushSync();

        expect(document.activeElement).toBe(opener);
    });

    it('falls back to the drive title when nothing was focused', () => {
        // A tap does not focus a button on either phone, so the opener is
        // usually the body and the title is where the reader should land.
        (document.activeElement as HTMLElement | null)?.blur();
        openDriveSwitcher();
        flushSync();
        closeDriveSwitcher();
        flushSync();

        expect(document.activeElement).toBe(opener);
    });

    it('stops the shell behind it answering, and puts it back', () => {
        openDriveSwitcher();
        flushSync();

        expect(behind.inert).toBe(true);
        // The scrim is the way out by tapping, so it stays live.
        expect((shell.querySelector('.sheet-scrim') as HTMLElement).inert).toBe(false);

        closeDriveSwitcher();
        flushSync();

        expect(behind.inert).toBe(false);
    });

    it('keeps Tab inside the sheet', () => {
        openDriveSwitcher();
        flushSync();

        const items = Array.from(sheet().querySelectorAll<HTMLElement>('button'));
        const last = items[items.length - 1];
        last.focus();
        const event = new KeyboardEvent('keydown', { key: 'Tab', bubbles: true, cancelable: true });
        window.dispatchEvent(event);

        expect(event.defaultPrevented).toBe(true);
        expect(document.activeElement).toBe(items[0]);
    });

    it('closes on Escape', () => {
        openDriveSwitcher();
        flushSync();

        window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }));
        flushSync();

        expect(get(driveSwitcherOpen)).toBe(false);
    });
});

describe('drag to dismiss', () => {
    it('closes when the sheet is pulled past the threshold', () => {
        openDriveSwitcher();
        flushSync();
        const grab = sheet().querySelector('.switcher-grab') as HTMLElement;

        pointer('pointerdown', grab, 100, 0);
        pointer('pointermove', grab, 320, 200);
        pointer('pointerup', grab, 320, 200);
        flushSync();

        expect(get(driveSwitcherOpen)).toBe(false);
        // The sheet is handed back to its own transition, not left mid-drag.
        expect(sheet().style.transform).toBe('');
    });

    it('springs back from a short pull', () => {
        openDriveSwitcher();
        flushSync();
        const grab = sheet().querySelector('.switcher-grab') as HTMLElement;

        pointer('pointerdown', grab, 100, 0);
        pointer('pointermove', grab, 112, 400);
        pointer('pointerup', grab, 112, 400);
        flushSync();

        expect(get(driveSwitcherOpen)).toBe(true);
        expect(sheet().style.transform).toBe('');
    });

    it('takes a flick as a dismissal even when it barely moved', () => {
        openDriveSwitcher();
        flushSync();
        const grab = sheet().querySelector('.switcher-grab') as HTMLElement;

        pointer('pointerdown', grab, 100, 0);
        pointer('pointermove', grab, 140, 16);
        pointer('pointerup', grab, 140, 16);
        flushSync();

        expect(get(driveSwitcherOpen)).toBe(false);
    });

    it('leaves the close button a button', () => {
        openDriveSwitcher();
        flushSync();
        const close = sheet().querySelector('.switcher-close') as HTMLElement;

        pointer('pointerdown', close, 100, 0);
        pointer('pointermove', close, 320, 200);
        pointer('pointerup', close, 320, 200);
        flushSync();

        expect(sheet().style.transform).toBe('');
        expect(get(driveSwitcherOpen)).toBe(true);
    });
});
