// The upload menu is a bottom sheet on a phone, and its grip says so: the
// comment beside it has always promised a downward swipe as one of the ways out.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, tick, unmount } from 'svelte';

vi.mock('../../api', () => ({ isMobilePlatform: () => true }));

import UploadMenu from './UploadMenu.svelte';
import { closeTopSheet } from '../modals/sheet-stack';

Object.defineProperty(HTMLElement.prototype, 'offsetParent', {
    configurable: true,
    get() {
        return this.parentElement;
    },
});

let target: HTMLElement;
let component: Record<string, unknown> | null = null;

function sheet(): HTMLElement {
    return target.querySelector('.upload-menu') as HTMLElement;
}

function pointer(type: string, el: EventTarget, clientY: number, timeStamp = 0): void {
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
    el.dispatchEvent(event);
    flushSync();
}

function openSheet(): HTMLElement {
    const trigger = target.querySelector('#upload-btn') as HTMLButtonElement;
    trigger.focus();
    trigger.click();
    flushSync();
    return target.querySelector('.sheet-handle') as HTMLElement;
}

beforeEach(() => {
    target = document.createElement('div');
    document.body.append(target);
    component = mount(UploadMenu, { target, props: { onFiles: vi.fn(), onNewFolder: vi.fn() } });
    flushSync();
});

afterEach(() => {
    if (component) unmount(component);
    component = null;
    target.remove();
});

describe('swipe down to dismiss', () => {
    it('closes on a pull past the threshold', () => {
        const handle = openSheet();
        expect(sheet().style.display).toBe('flex');

        pointer('pointerdown', handle, 100, 0);
        pointer('pointermove', handle, 340, 200);
        pointer('pointerup', handle, 340, 200);

        expect(sheet().style.display).toBe('none');
    });

    it('slides back from a pull that did not reach it', () => {
        const handle = openSheet();

        pointer('pointerdown', handle, 100, 0);
        pointer('pointermove', handle, 120, 400);
        pointer('pointerup', handle, 120, 400);

        expect(sheet().style.display).toBe('flex');
        expect(sheet().style.transform).toBe('');
    });

    it('follows the finger while it is down', () => {
        const handle = openSheet();

        pointer('pointerdown', handle, 100, 0);
        pointer('pointermove', handle, 160, 100);

        expect(sheet().style.transform).toBe('translateY(60px)');
    });
});

describe('Upload sheet accessibility', () => {
    it('acts as a named modal, keeps focus inside, and restores its trigger', async () => {
        const background = document.createElement('button');
        background.type = 'button';
        background.textContent = 'Background action';
        document.body.append(background);
        background.focus();

        openSheet();
        await tick();
        flushSync();

        const uploadSheet = sheet();
        const files = target.querySelector<HTMLButtonElement>('#upload-menu-files');
        const newFolder = target.querySelector<HTMLButtonElement>('#upload-menu-new-folder');
        const close = target.querySelector<HTMLButtonElement>('.upload-sheet-close');
        expect(uploadSheet.getAttribute('role')).toBe('dialog');
        expect(uploadSheet.getAttribute('aria-modal')).toBe('true');
        expect(uploadSheet.getAttribute('aria-label')).toBe('Upload options');
        expect(background.inert).toBe(true);
        expect(document.activeElement).toBe(files);

        // The Close control is the first focusable, so Tab from the last item
        // loops back to it rather than escaping the sheet.
        newFolder?.focus();
        const tab = new KeyboardEvent('keydown', { key: 'Tab', bubbles: true, cancelable: true });
        document.dispatchEvent(tab);
        expect(tab.defaultPrevented).toBe(true);
        expect(document.activeElement).toBe(close);

        document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }));
        await tick();
        flushSync();
        expect(uploadSheet.style.display).toBe('none');
        expect(background.inert).toBe(false);
        expect(document.activeElement).toBe(target.querySelector('#upload-btn'));
        background.remove();
    });

    it('dismisses from the Close control and restores the trigger', async () => {
        const trigger = target.querySelector<HTMLButtonElement>('#upload-btn');
        openSheet();
        await tick();
        flushSync();

        const close = target.querySelector<HTMLButtonElement>('.upload-sheet-close');
        expect(close?.getAttribute('aria-label')).toBe('Close');
        close?.click();
        await tick();
        flushSync();

        expect(sheet().style.display).toBe('none');
        expect(document.activeElement).toBe(trigger);
    });

    it('lets Android BACK dismiss the modal sheet', async () => {
        openSheet();
        await tick();
        expect(closeTopSheet()).toBe(true);
        await tick();
        flushSync();
        expect(sheet().style.display).toBe('none');
    });
});
