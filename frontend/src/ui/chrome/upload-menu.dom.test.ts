// The upload menu is a bottom sheet on a phone, and its grip says so: the
// comment beside it has always promised a downward swipe as one of the ways out.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { flushSync, mount, unmount } from 'svelte';

vi.mock('../../api', () => ({ isMobilePlatform: () => true }));

import UploadMenu from './UploadMenu.svelte';

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
    (target.querySelector('#upload-btn') as HTMLElement).click();
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
