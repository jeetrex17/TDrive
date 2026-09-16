import { afterEach, describe, expect, it, vi } from 'vitest';
import {
    deactivateModalOwnership,
    installModalA11y,
} from './modal-a11y';
import { closeTopSheet, hasOpenSheet } from './sheet-stack';

Object.defineProperty(HTMLElement.prototype, 'offsetParent', {
    configurable: true,
    get() {
        return this.parentElement;
    },
});

let elements: HTMLElement[] = [];
let cleanups: Array<() => void> = [];

function dialog(id: string): { host: HTMLElement; control: HTMLButtonElement } {
    const host = document.createElement('div');
    host.id = id;
    host.className = 'modal-overlay';
    const control = document.createElement('button');
    control.type = 'button';
    control.textContent = `${id} control`;
    host.appendChild(control);
    document.body.appendChild(host);
    elements.push(host);
    return { host, control };
}

afterEach(() => {
    cleanups.forEach((cleanup) => cleanup());
    cleanups = [];
    elements.forEach((element) => element.remove());
    elements = [];
    while (closeTopSheet());
});

describe('modal overlay ownership', () => {
    it('keeps only the top dialog interactive and dismisses it before its parent', () => {
        const background = document.createElement('button');
        background.type = 'button';
        background.textContent = 'Background action';
        document.body.appendChild(background);
        elements.push(background);
        background.focus();

        const parent = dialog('parent-modal');
        const child = dialog('child-modal');
        const closeParent = vi.fn();
        const closeChild = vi.fn();
        const parentA11y = installModalA11y(parent.host, {
            initialFocus: parent.control,
            requestClose: closeParent,
        });
        const childA11y = installModalA11y(child.host, {
            initialFocus: child.control,
            requestClose: closeChild,
        });
        cleanups.push(() => {
            childA11y.deactivate();
            parentA11y.deactivate();
            deactivateModalOwnership(child.host);
            deactivateModalOwnership(parent.host);
        });

        parentA11y.activate();
        expect(parent.host.inert).toBe(false);
        expect(background.inert).toBe(true);
        expect(parent.control).toBe(document.activeElement);

        childA11y.activate();
        expect(parent.host.inert).toBe(true);
        expect(parent.host.getAttribute('aria-hidden')).toBe('true');
        expect(child.host.inert).toBe(false);
        expect(child.host.getAttribute('aria-hidden')).toBe('false');
        expect(child.control).toBe(document.activeElement);

        document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }));
        expect(closeChild).toHaveBeenCalledTimes(1);
        expect(closeParent).not.toHaveBeenCalled();

        childA11y.deactivate();
        expect(parent.host.inert).toBe(false);
        expect(parent.host.getAttribute('aria-hidden')).toBe('false');
        expect(parent.control).toBe(document.activeElement);

        parentA11y.deactivate();
        expect(background.inert).toBe(false);
        expect(background.getAttribute('aria-hidden')).toBeNull();
    });

    it('does not restore background focus when a covered dialog closes', () => {
        const background = document.createElement('button');
        document.body.appendChild(background);
        elements.push(background);
        background.focus();
        const parent = dialog('parent');
        const child = dialog('child');
        const parentA11y = installModalA11y(parent.host, { initialFocus: parent.control });
        const childA11y = installModalA11y(child.host, { initialFocus: child.control });
        cleanups.push(() => { childA11y.deactivate(); parentA11y.deactivate(); });
        parentA11y.activate();
        childA11y.activate();
        parentA11y.deactivate();
        expect(document.activeElement).toBe(child.control);
        expect(background.inert).toBe(true);
    });

    // Android BACK is answered from the same place Escape is, so a dialog that
    // can be dismissed at all can be dismissed with the hardware button.
    it('answers android back for as long as it is open', () => {
        const modal = dialog('confirm');
        const close = vi.fn();
        const a11y = installModalA11y(modal.host, { requestClose: close });
        cleanups.push(() => a11y.deactivate());

        expect(hasOpenSheet()).toBe(false);
        a11y.activate();
        expect(closeTopSheet()).toBe(true);
        expect(close).toHaveBeenCalledOnce();

        a11y.deactivate();
        expect(hasOpenSheet()).toBe(false);
    });

    it('keeps answering back when a press only dismissed an inner layer', () => {
        const modal = dialog('player');
        let playlistOpen = true;
        const closePlayer = vi.fn();
        const a11y = installModalA11y(modal.host, {
            requestClose: () => {
                if (playlistOpen) {
                    playlistOpen = false;
                    return;
                }
                closePlayer();
            },
        });
        cleanups.push(() => a11y.deactivate());
        a11y.activate();

        // First press closes the playlist; the player is still the surface the
        // next press belongs to.
        expect(closeTopSheet()).toBe(true);
        expect(playlistOpen).toBe(false);
        expect(closePlayer).not.toHaveBeenCalled();

        expect(closeTopSheet()).toBe(true);
        expect(closePlayer).toHaveBeenCalledOnce();
    });

    it('stacks nested dialogs so back unwinds them one at a time', () => {
        const parent = dialog('parent-back');
        const child = dialog('child-back');
        const closeParent = vi.fn();
        const closeChild = vi.fn();
        const parentA11y = installModalA11y(parent.host, { requestClose: closeParent });
        const childA11y = installModalA11y(child.host, { requestClose: closeChild });
        cleanups.push(() => { childA11y.deactivate(); parentA11y.deactivate(); });
        parentA11y.activate();
        childA11y.activate();

        expect(closeTopSheet()).toBe(true);
        expect(closeChild).toHaveBeenCalledOnce();
        expect(closeParent).not.toHaveBeenCalled();
    });

    it('stays out of the back stack when it has no close path', () => {
        const modal = dialog('static');
        const a11y = installModalA11y(modal.host, {});
        cleanups.push(() => a11y.deactivate());
        a11y.activate();
        expect(hasOpenSheet()).toBe(false);
    });

    it('skips inert controls when choosing initial focus', () => {
        const modal = dialog('media');
        const hiddenControls = document.createElement('div');
        hiddenControls.inert = true;
        const hiddenButton = document.createElement('button');
        hiddenControls.appendChild(hiddenButton);
        modal.host.prepend(hiddenControls);
        const a11y = installModalA11y(modal.host, { initialFocus: hiddenButton });
        cleanups.push(() => a11y.deactivate());
        a11y.activate();
        expect(document.activeElement).toBe(modal.control);
    });
});
